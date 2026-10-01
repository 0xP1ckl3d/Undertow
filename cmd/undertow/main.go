package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/netstack"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/relay"
	"undertow/internal/tun"
)

var version = "dev"
var commit = "none"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) < 2 || os.Getenv(backgroundModeEnv) != os.Args[1] {
		writeBanner(os.Stderr)
	}
	if len(os.Args) < 2 {
		_ = writeHelp(os.Stderr, "")
		os.Exit(2)
	}
	if os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		topic := ""
		if len(os.Args) > 2 {
			topic = os.Args[2]
		}
		if err := writeHelp(os.Stdout, topic); err != nil {
			log.Println("error:", err)
			os.Exit(2)
		}
		return
	}
	if helpRequested(os.Args[2:]) {
		if err := writeHelp(os.Stdout, os.Args[1]); err != nil {
			log.Println("error:", err)
			os.Exit(2)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = initKeys(os.Args[2:])
	case "server":
		err = serve(os.Args[2:])
	case "agent":
		if len(os.Args) > 2 && (os.Args[2] == "list" || os.Args[2] == "show" || os.Args[2] == "select") {
			err = agentCommand(os.Args[2:])
		} else {
			err = agent(os.Args[2:])
		}
	case "client":
		err = clientCommand(os.Args[2:])
	case "status":
		err = statusCommand(os.Args[2:])
	case "console":
		err = consoleCommand(os.Args[2:])
	case "route":
		err = routeCommand(os.Args[2:])
	case "session":
		err = sessionCommand(os.Args[2:])
	case "version":
		fmt.Printf("undertow %s (%s)\n", version, commit)
	case "examples":
		if len(os.Args) != 2 {
			err = errors.New("usage: undertow examples")
		} else {
			err = writeExamples(os.Stdout)
		}
	case "doctor":
		err = doctorCommand(os.Args[2:], os.Stdout)
	default:
		_ = writeHelp(os.Stderr, "")
		os.Exit(2)
	}
	if err != nil {
		log.Println("error:", err)
		os.Exit(1)
	}
}

type forwards []string

type advertisedRoutes []string

func (r *advertisedRoutes) String() string { return strings.Join(*r, ",") }
func (r *advertisedRoutes) Set(value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("--advertise-route requires an IPv4 CIDR")
	}
	if len(*r) >= 16 {
		return errors.New("at most 16 explicit routes can be advertised")
	}
	*r = append(*r, prefix.Masked().String())
	return nil
}

func (f *forwards) String() string { return strings.Join(*f, ",") }
func (f *forwards) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return errors.New("forward must be listen=destination")
	}
	for _, part := range parts {
		if _, _, err := net.SplitHostPort(part); err != nil {
			return err
		}
	}
	*f = append(*f, value)
	return nil
}

func initKeys(args []string) error {
	f := flag.NewFlagSet("init", flag.ContinueOnError)
	identity := f.String("identity", "identity.key", "server identity key file")
	tokenFile := f.String("token-file", "token.key", "enrolment token file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected init argument %q; put options after 'init'", f.Arg(0))
	}
	k, err := security.LoadOrCreateKey(*identity)
	if err != nil {
		return err
	}
	if _, err = os.Stat(*tokenFile); errors.Is(err, os.ErrNotExist) {
		var token [32]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		if err = os.WriteFile(*tokenFile, []byte(hex.EncodeToString(token[:])+"\n"), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	fmt.Println("server fingerprint:", security.Fingerprint(k))
	fmt.Println("identity:", *identity, "token file:", *tokenFile)
	return nil
}

func serve(args []string) error {
	if len(args) > 0 && args[0] == "attach" {
		return attachServer(args[1:])
	}
	f := flag.NewFlagSet("server", flag.ContinueOnError)
	lifecycle := addLifecycleFlags(f, "server")
	carrier := addCarrierFlags(f, "dns,websocket,quic")
	listen := f.String("listen", "", "carrier listen address (default DNS UDP :53, WebSocket TCP :443, QUIC UDP :443)")
	dnsListen := f.String("dns-listen", "", "DNS UDP listen address for multi-transport server")
	websocketListen := f.String("websocket-listen", "", "WebSocket TCP listen address for multi-transport server")
	quicListen := f.String("quic-listen", "", "QUIC UDP listen address for multi-transport server")
	domain := f.String("domain", "t.undertow.invalid", "synthetic DNS domain")
	identityPath := f.String("identity", "identity.key", "server identity key file")
	tokenPath := f.String("token-file", "", "enrolment token file (default token.key)")
	tokenValue := f.String("token", "", "enrolment token hex; visible in process listings")
	authMode := f.String("auth", "token", "client enrollment: token, password, none")
	password := f.String("password", "", "shared enrollment password; visible in process listings")
	passwordFile := f.String("password-file", "", "read shared enrollment password from a file")
	probeEcho := f.Bool("probe-echo", false, "echo Phase 1 diagnostic probes instead of running TCP streams")
	viaAgent := f.String("via-agent", "", "agent ID for local TCP forwards; optional only with one agent")
	enableTun := f.Bool("tun", false, "enable server proxy TUN/Wintun and route control")
	tunName := f.String("tun-name", "undertow0", "proxy TUN interface name")
	tunnelAddress := f.String("tunnel-address", "172.16.254.1/24", "proxy TUN IPv4 address/prefix")
	controlListen := f.String("control-listen", "127.0.0.1:47889", "loopback operator control API")
	controlTokenPath := f.String("control-token-file", "control.key", "local operator API token file")
	var forwardValues forwards
	f.Var(&forwardValues, "forward", "local TCP forward listen=remote-destination; repeatable")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected server argument %q; use 'undertow server --listen ...'", f.Arg(0))
	}
	var selectedTransports []string
	selectedSet := make(map[string]bool)
	for _, name := range strings.Split(*carrier.kind, ",") {
		kind, err := canonicalTransport(name)
		if err != nil {
			return err
		}
		if !selectedSet[kind] {
			selectedTransports = append(selectedTransports, kind)
			selectedSet[kind] = true
		}
	}
	if *listen != "" && len(selectedTransports) != 1 {
		return errors.New("--listen requires one selected transport; use --dns-listen, --websocket-listen, or --quic-listen")
	}
	if (*carrier.cert == "") != (*carrier.key == "") {
		return errors.New("--tls-cert and --tls-key must be supplied together")
	}
	if *carrier.selfSigned && *carrier.cert != "" {
		return errors.New("--tls-self-signed cannot be combined with certificate files")
	}
	if !selectedSet["websocket"] && !selectedSet["quic"] && (*carrier.cert != "" || *carrier.selfSigned || *carrier.serverName != "" || *carrier.skipTLSVerify) {
		return errors.New("TLS options require a WebSocket or QUIC server listener")
	}
	for _, option := range []struct{ kind, address string }{{"dns", *dnsListen}, {"websocket", *websocketListen}, {"quic", *quicListen}} {
		if option.address != "" && !selectedSet[option.kind] {
			return fmt.Errorf("--%s-listen requires %s in --transport", option.kind, option.kind)
		}
		if *listen != "" && option.address != "" {
			return errors.New("choose --listen or a per-transport listen flag")
		}
	}
	if !*lifecycle.background && !*lifecycle.foreground && !*lifecycle.stop && os.Getenv(backgroundModeEnv) != "server" && isConsoleTerminal(os.Stdin) {
		logPath, err := filepath.Abs(*lifecycle.logFile)
		if err != nil {
			return err
		}
		pidPath, err := filepath.Abs(*lifecycle.pidFile)
		if err != nil {
			return err
		}
		if err := launchBackground("server", args, logPath, pidPath); err != nil {
			return err
		}
		return attachServerAt(pidPath, *controlListen, *controlTokenPath, logPath)
	}
	handled, cleanup, err := lifecycle.handle(args)
	if err != nil || handled {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	identity, err := security.LoadOrCreateKey(*identityPath)
	if err != nil {
		return err
	}
	token, err := security.EnrollmentSecret(*authMode, *tokenPath, *tokenValue, *password, *passwordFile, security.Fingerprint(identity))
	if err != nil {
		return err
	}
	log.Printf("client enrollment mode: %s", *authMode)
	if *authMode == "none" {
		log.Print("WARNING: --auth none allows anyone who can reach this server to enroll, access the network, run commands, and transfer files on agents unless agents restrict those capabilities with --deny; use token or password enrollment for real deployments")
	}
	localNetworks, err := tun.ExistingNetworks()
	if err != nil {
		return err
	}
	proxyPrefix, err := netip.ParsePrefix(*tunnelAddress)
	if err != nil || !proxyPrefix.Addr().Is4() {
		return errors.New("--tunnel-address must be an IPv4 prefix")
	}
	proxyNetwork := proxyPrefix.Masked()
	for _, p := range localNetworks {
		if *enableTun && (p.Contains(proxyNetwork.Addr()) || proxyNetwork.Contains(p.Addr())) {
			return fmt.Errorf("tunnel network %s conflicts with local network %s", proxyNetwork, p)
		}
	}
	localNetworks = append(localNetworks, proxyNetwork)
	var device *tun.Device
	if *enableTun {
		device, err = tun.Open(*tunName, *tunnelAddress)
		if err != nil {
			return err
		}
		defer device.Close()
		log.Printf("proxy TUN %s address %s", device.Name(), *tunnelAddress)
	} else {
		log.Print("proxy TUN disabled; VPN socket egress remains available")
	}
	table := routing.New(localNetworks)
	var routeDevice control.RouteDevice
	if device != nil {
		routeDevice = device
	}
	manager := control.NewManager(table, routeDevice, proxyNetwork, proxyPrefix.Addr())
	controlToken, err := control.LoadOrCreateToken(*controlTokenPath)
	if err != nil {
		return err
	}
	ctx, stop := commandContext()
	defer stop()
	serverInfo := control.ServerInfo{Transport: *carrier.kind, Domain: *domain, WebSocketPath: *carrier.path, Fingerprint: security.Fingerprint(identity)}
	manager.SetServerInfo(serverInfo)
	transportManager := newServerTransports(ctx, manager, identity, token, *domain, *carrier.path, func(peer transport.Peer) {
		handleServerPeer(ctx, manager, controlToken, *probeEcho, peer)
	})
	manager.SetTransportController(transportManager)
	manager.SetRelayAcceptor(func(_ context.Context, parentID string, stream *mux.Stream) {
		peer, err := relay.Accept(ctx, stream, parentID, identity, token)
		if err != nil {
			log.Printf("relay via %s rejected: %v", parentID, err)
			_ = stream.Close()
			return
		}
		handleServerPeer(ctx, manager, controlToken, *probeEcho, peer)
	})
	defer transportManager.Close()
	go func() {
		if err := manager.ServeHTTP(ctx, *controlListen, controlToken); err != nil && ctx.Err() == nil {
			log.Printf("control API: %v", err)
			stop()
		}
	}()
	if device != nil {
		go func() {
			if err := netstack.Serve(ctx, device, proxyPrefix, manager.Choose); err != nil && ctx.Err() == nil {
				log.Printf("proxy netstack: %v", err)
				stop()
			}
		}()
	}
	choose := func() *mux.Mux {
		if *viaAgent != "" {
			return manager.Get(*viaAgent)
		}
		return manager.Only()
	}
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	for _, value := range forwardValues {
		parts := strings.SplitN(value, "=", 2)
		l, err := net.Listen("tcp", parts[0])
		if err != nil {
			transportManager.Close()
			return err
		}
		listeners = append(listeners, l)
		log.Printf("local forward %s -> %s", l.Addr(), parts[1])
		go pivot.ServeForward(ctx, l, parts[1], choose)
	}
	for _, kind := range selectedTransports {
		request := control.TransportStartRequest{TLSCert: *carrier.cert, TLSKey: *carrier.key}
		switch kind {
		case "dns":
			request.Listen = *dnsListen
			request.TLSCert, request.TLSKey = "", ""
		case "websocket":
			request.Listen = *websocketListen
		case "quic":
			request.Listen = *quicListen
		}
		if *listen != "" {
			request.Listen = *listen
		}
		info, err := transportManager.Start(kind, request)
		if err != nil {
			return err
		}
		if len(selectedTransports) == 1 {
			serverInfo.Transport, serverInfo.Network, serverInfo.Listen, serverInfo.TLSMode = info.Transport, info.Network, info.Listen, info.TLSMode
		}
	}
	serverInfo.Listeners = transportManager.List()
	manager.SetServerInfo(serverInfo)
	log.Printf("server fingerprint %s", security.Fingerprint(identity))
	if err := markBackgroundReady(); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func echo(ctx context.Context, p transport.Peer) {
	for {
		b, err := p.Channel().Recv(ctx)
		if err != nil {
			return
		}
		if err = p.Channel().Send(ctx, b); err != nil {
			return
		}
	}
}

func agent(args []string) error {
	f := flag.NewFlagSet("agent", flag.ContinueOnError)
	lifecycle := addLifecycleFlags(f, "agent")
	carrier := addCarrierFlags(f)
	server := f.String("server", "", "carrier server host:port (numeric IPv4 for DNS)")
	domain := f.String("domain", "t.undertow.invalid", "synthetic DNS domain")
	fingerprint := f.String("fingerprint", "", "pinned SHA-256 server public-key fingerprint")
	fingerprintFile := f.String("fingerprint-file", "server.fingerprint", "saved server fingerprint")
	trustFirstUse := f.Bool("trust-on-first-use", false, "save server fingerprint after first authenticated connection")
	tokenPath := f.String("token-file", "", "enrolment token file (default token.key)")
	tokenValue := f.String("token", "", "enrolment token hex; visible in process listings")
	authMode := f.String("auth", "token", "client enrollment: token, password, none")
	password := f.String("password", "", "shared enrollment password; visible in process listings")
	passwordFile := f.String("password-file", "", "read shared enrollment password from a file")
	keyPath := f.String("agent-key", "agent.key", "agent identity key file")
	probeSize := f.Int("probe-size", 64, "encrypted echo probe payload bytes")
	probeCount := f.Uint64("probe-count", 0, "number of probes; 0 runs until stopped")
	interval := f.Duration("probe-interval", time.Second, "time between probes; 0 sends as fast as the window allows")
	profileFlag := f.String("payload-profile", "auto", "DNS payload profile: auto, large, or small")
	probe := f.Bool("probe", false, "run Phase 1 echo probes instead of TCP socket handling")
	deny := f.String("deny", "", "comma-separated agent capabilities to disable: pivot,exec,hostops,interactive,scripts,wasm,native,upload,download,listeners")
	var advertise advertisedRoutes
	f.Var(&advertise, "advertise-route", "IPv4 CIDR offered for client acceptance; repeatable")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected agent argument %q; use 'undertow agent --server ...'", f.Arg(0))
	}
	caps, err := pivot.ParseDenied(*deny)
	if err != nil {
		return err
	}
	handled, cleanup, err := lifecycle.handle(args)
	if err != nil || handled {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if *server == "" {
		return errors.New("--server is required")
	}
	if *probeSize < 16 || *probeSize > 64<<10 {
		return errors.New("--probe-size must be between 16 and 65536")
	}
	if *profileFlag != "auto" && *profileFlag != "large" && *profileFlag != "small" {
		return errors.New("invalid --payload-profile")
	}
	if *carrier.kind != "dns" && *profileFlag != "auto" {
		return errors.New("--payload-profile applies only to DNS transport")
	}
	ctx, stop := commandContext()
	defer stop()
	pinnedFingerprint, savePin, err := carrier.fingerprint(ctx, *server, *domain, *fingerprint, *fingerprintFile, *trustFirstUse)
	if err != nil {
		return err
	}
	token, err := security.EnrollmentSecret(*authMode, *tokenPath, *tokenValue, *password, *passwordFile, pinnedFingerprint)
	if err != nil {
		return err
	}
	key, err := security.LoadOrCreateKey(*keyPath)
	if err != nil {
		return err
	}
	for {
		c, err := carrier.dial(ctx, *server, *domain, pinnedFingerprint, token, key, *profileFlag)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("connect failed: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}
		if savePin {
			if err := saveServerFingerprint(*fingerprintFile, pinnedFingerprint); err != nil {
				c.Close()
				return fmt.Errorf("save server fingerprint: %w", err)
			}
			log.Printf("trusted server fingerprint saved to %s: %s", *fingerprintFile, pinnedFingerprint)
			savePin = false
		}
		if err := markBackgroundReady(); err != nil {
			c.Close()
			return err
		}
		if dnsClient, ok := c.(*dns.Client); ok {
			log.Printf("connected: session=%d agent=%s fragment=%d", c.ID(), security.Fingerprint(key), dnsClient.Session.Stats().FragmentSize)
		} else {
			log.Printf("connected: session=%d agent=%s transport=%s", c.ID(), security.Fingerprint(key), *carrier.kind)
		}
		if *probe || *probeCount > 0 {
			err = runProbes(ctx, c, *probeSize, *probeCount, *interval)
		} else {
			streamMux := mux.New(ctx, c, false)
			if sendErr := control.SendInventory(ctx, streamMux, advertise, caps); sendErr != nil {
				log.Printf("inventory: %v", sendErr)
			}
			pivot.ServeAgentWithCapabilities(ctx, streamMux, caps)
			streamMux.Close()
			err = io.EOF
		}
		if dnsClient, ok := c.(*dns.Client); ok {
			stats, q, r := dnsClient.Stats()
			adaptive := dnsClient.AdaptiveStats()
			log.Printf("session finished: queries=%d responses=%d tx=%d rx=%d retransmits=%d rtt=%s fragment=%d payload_adjustments=%d queued=%d in_flight=%d cwnd=%d peer_window=%d dns_outstanding=%d dns_target=%d dns_health_window=%d", q, r, stats.TXBytes, stats.RXBytes, stats.Retransmits, stats.RTT, stats.FragmentSize, stats.PayloadAdjustments, stats.Queued, stats.InFlight, stats.CongestionWindow, stats.PeerReceiveWindow, adaptive.Outstanding, adaptive.Target, adaptive.HealthWindow)
		} else {
			log.Printf("session finished: transport=%s", *carrier.kind)
		}
		c.Close()
		if *probeCount > 0 && err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("reconnecting after %v", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

func runProbes(ctx context.Context, c transport.Connection, size int, count uint64, interval time.Duration) error {
	var sessionDone <-chan struct{}
	if dnsClient, ok := c.(*dns.Client); ok {
		sessionDone = dnsClient.Session.Done()
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var sent, received atomic.Uint64
	window := make(chan struct{}, 32)
	errCh := make(chan error, 1)
	go func() {
		for {
			b, err := c.Recv(probeCtx)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			if len(b) != size {
				select {
				case errCh <- errors.New("echo size mismatch"):
				default:
				}
				return
			}
			seq := binary.BigEndian.Uint64(b[:8])
			start := int64(binary.BigEndian.Uint64(b[8:16]))
			if seq == 0 || start == 0 {
				select {
				case errCh <- errors.New("invalid echo"):
				default:
				}
				return
			}
			n := received.Add(1)
			if n == 1 || n%100 == 0 || count > 0 && n == count {
				log.Printf("echo %d/%d RTT=%s", n, sent.Load(), time.Since(time.Unix(0, start)))
			}
			select {
			case <-window:
			default:
			}
		}
	}()
	for count == 0 || sent.Load() < count {
		select {
		case <-ctx.Done():
			return nil
		case <-sessionDone:
			return io.EOF
		case err := <-errCh:
			return err
		case window <- struct{}{}:
		}
		b := make([]byte, size)
		seq := sent.Add(1)
		binary.BigEndian.PutUint64(b[:8], seq)
		binary.BigEndian.PutUint64(b[8:16], uint64(time.Now().UnixNano()))
		if _, err := rand.Read(b[16:]); err != nil {
			return err
		}
		if err := c.Send(ctx, b); err != nil {
			return err
		}
		if interval > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(interval):
			}
		}
	}
	for received.Load() < count {
		select {
		case <-ctx.Done():
			return nil
		case <-sessionDone:
			return io.EOF
		case err := <-errCh:
			return err
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}
