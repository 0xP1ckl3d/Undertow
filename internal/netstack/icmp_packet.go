//go:build linux || windows

package netstack

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net/netip"
	"time"

	"undertow/internal/icmp"
	"undertow/internal/mux"
)

func packetChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func makeIPv4ICMP(source, destination netip.Addr, body []byte) []byte {
	packet := make([]byte, 20+len(body))
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[8] = 64
	packet[9] = 1
	a, b := source.As4(), destination.As4()
	copy(packet[12:16], a[:])
	copy(packet[16:20], b[:])
	copy(packet[20:], body)
	binary.BigEndian.PutUint16(packet[10:12], packetChecksum(packet[:20]))
	return packet
}

func echoReply(request []byte, headerLen, total int) []byte {
	body := append([]byte(nil), request[headerLen:total]...)
	body[0] = 0
	body[2], body[3] = 0, 0
	binary.BigEndian.PutUint16(body[2:4], packetChecksum(body))
	source := netip.AddrFrom4([4]byte{request[16], request[17], request[18], request[19]})
	destination := netip.AddrFrom4([4]byte{request[12], request[13], request[14], request[15]})
	return makeIPv4ICMP(source, destination, body)
}

func hostUnreachable(request []byte, headerLen, total int, source netip.Addr) []byte {
	quoteLen := headerLen + 8
	if quoteLen > total {
		quoteLen = total
	}
	body := make([]byte, 8+quoteLen)
	body[0], body[1] = 3, 1
	copy(body[8:], request[:quoteLen])
	binary.BigEndian.PutUint16(body[2:4], packetChecksum(body))
	destination := netip.AddrFrom4([4]byte{request[12], request[13], request[14], request[15]})
	return makeIPv4ICMP(source, destination, body)
}

// handleICMPEcho returns false for non-echo packets. Echo reachability is
// checked by the unprivileged agent before constructing a proxy-side reply.
func handleICMPEcho(ctx context.Context, raw []byte, proxyIP netip.Addr, choose Selector, write func([]byte) error) bool {
	return handleICMPEchoResolved(ctx, raw, proxyIP, func(ip netip.Addr) (*mux.Mux, bool) { return choose(ip), true }, write)
}

func handleICMPEchoResolved(ctx context.Context, raw []byte, proxyIP netip.Addr, choose EgressSelector, write func([]byte) error) bool {
	if len(raw) < 28 || raw[0]>>4 != 4 || raw[9] != 1 {
		return false
	}
	headerLen := int(raw[0]&15) * 4
	total := int(binary.BigEndian.Uint16(raw[2:4]))
	if headerLen < 20 || total > len(raw) || total < headerLen+8 {
		return false
	}
	if binary.BigEndian.Uint16(raw[6:8])&0x3fff != 0 {
		return false
	}
	if raw[headerLen] != 8 || raw[headerLen+1] != 0 {
		return false
	}
	request := append([]byte(nil), raw[:total]...)
	target := netip.AddrFrom4([4]byte{raw[16], raw[17], raw[18], raw[19]})
	go func() {
		selected, restricted := choose(target)
		if restricted && selected == nil {
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		payload := request[headerLen+8 : total]
		if len(payload) == 0 {
			payload = []byte{0}
		}
		if len(payload) > 1400 {
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		if !restricted {
			if _, err := icmp.Echo(callCtx, target, payload); err != nil {
				_ = write(hostUnreachable(request, headerLen, total, proxyIP))
				return
			}
			_ = write(echoReply(request, headerLen, total))
			return
		}
		stream, err := selected.Open(callCtx, "icmp://"+target.String()+":0")
		if err != nil {
			log.Printf("ICMP pivot open %s failed: %v", target, err)
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		defer stream.Close()
		if _, err = stream.Write(payload); err != nil {
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		if err = stream.CloseWrite(); err != nil {
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		status := make([]byte, 1)
		readDone := make(chan error, 1)
		go func() { _, e := io.ReadFull(stream, status); readDone <- e }()
		select {
		case err = <-readDone:
		case <-callCtx.Done():
			err = callCtx.Err()
		}
		if err != nil || status[0] != 0 {
			log.Printf("ICMP pivot echo %s failed: status=%d err=%v", target, status[0], err)
			_ = write(hostUnreachable(request, headerLen, total, proxyIP))
			return
		}
		_ = write(echoReply(request, headerLen, total))
	}()
	return true
}
