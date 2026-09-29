//go:build linux || windows

package netstack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type Selector func(destination netip.Addr) *mux.Mux
type EgressSelector func(destination netip.Addr) (*mux.Mux, bool)

// Serve translates packets from an operator TUN into selected agent flows.
func Serve(parent context.Context, device io.ReadWriteCloser, proxyAddress netip.Prefix, choose Selector) error {
	return serve(parent, device, proxyAddress, func(ip netip.Addr) (*mux.Mux, bool) { return choose(ip), true })
}

// ServeEgress handles VPN client packets: configured pivot routes use agents,
// and destinations without a pivot route use ordinary server-side sockets.
func ServeEgress(parent context.Context, device io.ReadWriteCloser, proxyAddress netip.Prefix, choose EgressSelector) error {
	return serve(parent, device, proxyAddress, choose)
}

func serve(parent context.Context, device io.ReadWriteCloser, proxyAddress netip.Prefix, choose EgressSelector) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if !proxyAddress.Addr().Is4() {
		return errors.New("proxy address must be IPv4")
	}
	s := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	defer s.Close()
	ep := channel.New(1024, 1400, "")
	defer ep.Close()
	if err := s.CreateNIC(1, ep); err != nil {
		return fmt.Errorf("create stack NIC: %s", err)
	}
	addr := proxyAddress.Addr().As4()
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4(addr), PrefixLen: proxyAddress.Bits()}}, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("add stack address: %s", err)
	}
	if err := s.SetPromiscuousMode(1, true); err != nil {
		return fmt.Errorf("enable stack promiscuity: %s", err)
	}
	if err := s.SetSpoofing(1, true); err != nil {
		return fmt.Errorf("enable stack spoofing: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	var firstTCP sync.Once
	forwarder := tcp.NewForwarder(s, 0, 1024, func(req *tcp.ForwarderRequest) {
		id := req.ID()
		ip, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice())
		if !ok {
			req.Complete(true)
			return
		}
		selected, restricted := choose(ip)
		if restricted && selected == nil {
			req.Complete(true)
			return
		}
		openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		destination := net.JoinHostPort(ip.String(), strconv.Itoa(int(id.LocalPort)))
		firstTCP.Do(func() { log.Printf("first TUN TCP flow: %s -> %s", id.RemoteAddress, destination) })
		var stream *mux.Stream
		var remote net.Conn
		var err error
		if selected != nil {
			stream, err = selected.Open(openCtx, destination)
		} else {
			var dialer net.Dialer
			remote, err = dialer.DialContext(openCtx, "tcp", destination)
		}
		if err != nil {
			log.Printf("TCP egress dial %s failed: %v", destination, err)
			req.Complete(true)
			return
		}
		var queue waiter.Queue
		endpoint, stackErr := req.CreateEndpoint(&queue)
		if stackErr != nil {
			log.Printf("TCP stack endpoint %s failed: %v", destination, stackErr)
			req.Complete(true)
			if stream != nil {
				stream.Close()
			}
			if remote != nil {
				remote.Close()
			}
			return
		}
		req.Complete(false)
		if stream != nil {
			pivot.Bridge(ctx, gonet.NewTCPConn(&queue, endpoint), stream)
		} else {
			pivot.BridgeConn(ctx, gonet.NewTCPConn(&queue, endpoint), remote)
		}
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, forwarder.HandlePacket)
	udpForwarder := udp.NewForwarder(s, func(req *udp.ForwarderRequest) {
		id := req.ID()
		var queue waiter.Queue
		endpoint, stackErr := req.CreateEndpoint(&queue)
		if stackErr != nil {
			return
		}
		go func() {
			ip, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice())
			if !ok {
				endpoint.Close()
				return
			}
			selected, restricted := choose(ip)
			if restricted && selected == nil {
				endpoint.Close()
				return
			}
			openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			destination := net.JoinHostPort(ip.String(), strconv.Itoa(int(id.LocalPort)))
			var stream *mux.Stream
			var remote net.Conn
			var err error
			if selected != nil {
				stream, err = selected.Open(openCtx, "udp://"+destination)
			} else {
				var dialer net.Dialer
				remote, err = dialer.DialContext(openCtx, "udp", destination)
			}
			if err != nil {
				endpoint.Close()
				return
			}
			if stream != nil {
				pivot.BridgeUDP(ctx, gonet.NewUDPConn(&queue, endpoint), stream)
			} else {
				pivot.BridgeUDPConn(ctx, gonet.NewUDPConn(&queue, endpoint), remote)
			}
		}()
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)
	readErr := make(chan error, 1)
	var writeMu sync.Mutex
	writePacket := func(packet []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err := device.Write(packet)
		return err
	}
	go func() {
		packet := make([]byte, 65535)
		for {
			n, err := device.Read(packet)
			if err != nil {
				readErr <- err
				cancel()
				return
			}
			if n < 20 || packet[0]>>4 != 4 {
				continue
			}
			if handleICMPEchoResolved(ctx, packet[:n], proxyAddress.Addr(), choose, writePacket) {
				continue
			}
			buf := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), packet[:n]...))})
			ep.InjectInbound(ipv4.ProtocolNumber, buf)
			buf.DecRef()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-readErr:
				return err
			default:
				return nil
			}
		case err := <-readErr:
			return err
		default:
		}
		packet := ep.ReadContext(ctx)
		if packet == nil {
			select {
			case err := <-readErr:
				return err
			default:
				return nil
			}
		}
		view := packet.ToView()
		err := writePacket(view.AsSlice())
		view.Release()
		packet.DecRef()
		if err != nil {
			return err
		}
	}
}
