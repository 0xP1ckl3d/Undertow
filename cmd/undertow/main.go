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
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	agentruntime "undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/deployment"
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
	if len(os.Args) >= 2 && os.Args[1] == "_bof-worker" {
		if err := bof.WorkerMain(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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
	case "operators":
		err = operatorAccountsCommand(os.Args[2:])
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
	case "bof":
		err = bofCommand(os.Args[2:], os.Stdout)
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
	agentStorePath := f.String("agent-store", "agent-distribution", "agent profile and artifact store directory")
	jobOutputDir := f.String("job-output-dir", "jobs-output", "server directory for background job output, nested by agent")
	operationsDB := f.String("operations-db", "operations.db", "server audit and retained operations database")
	jobOutputLimitMiB := f.Uint64("job-output-limit-mib", 512, "maximum output per background job in MiB")
	jobOutputTotalMiB := f.Uint64("job-output-total-mib", 4096, "maximum stored job output across all agents in MiB")
	agentTemplatePath := f.String("agent-templates", "", "directory containing prebuilt thin-agent templates (default: alongside undertow executable)")
	payloadRetrievalPath := f.String("payload-retrieval-path", "/", "opaque payload download path prefix (default: /)")
	var forwardValues forwards
	f.Var(&forwardValues, "forward", "local TCP forward listen=remote-destination; repeatable")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected server argument %q; use 'undertow server --listen ...'", f.Arg(0))
	}
	if err := carrier.load(f); err != nil {
		return err
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
	if !validRetrievalPath(*payloadRetrievalPath) {
		return errors.New("--payload-retrieval-path must be / or a clean absolute prefix ending in /")
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
	workerLogs := control.NewWorkerLogBuffer()
	previousLogOutput := log.Writer()
	log.SetOutput(io.MultiWriter(previousLogOutput, workerLogs))
	defer log.SetOutput(previousLogOutput)
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
		log.Print("WARNING: --auth none permits open agent enrollment; operator clients still require server-managed account credentials")
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
	manager.SetWorkerLogs(workerLogs)
	workerLogs.OnWrite(func() { manager.PublishEvent("worker_log", "server") })
	operations, err := control.OpenOperationsStore(*operationsDB)
	if err != nil {
		return err
	}
	defer operations.Close()
	accountCount, err := operations.OperatorCount()
	if err != nil {
		return err
	}
	if accountCount == 0 {
		return errors.New("no operator accounts: run 'undertow operators bootstrap --operations-db PATH' before starting the server")
	}
	if err := manager.SetOperationsStore(operations); err != nil {
		return fmt.Errorf("load agent history: %w", err)
	}
	publicHost, err := operations.PublicHost()
	if err != nil {
		return fmt.Errorf("load public host: %w", err)
	}
	if *jobOutputLimitMiB == 0 || *jobOutputTotalMiB < *jobOutputLimitMiB || *jobOutputTotalMiB > ^uint64(0)/(1<<20) {
		return errors.New("invalid job output limits")
	}
	if err := manager.ConfigureJobOutput(*jobOutputDir, *jobOutputLimitMiB<<20, *jobOutputTotalMiB<<20); err != nil {
		return err
	}
	if err := manager.RestoreJobHistory(); err != nil {
		return err
	}
	templatePath := *agentTemplatePath
	if templatePath == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		templatePath = filepath.Dir(executable)
	}
	buildVersion := version
	if commit != "none" {
		buildVersion += " (" + commit + ")"
	}
	distributionStore, err := agentprofile.OpenStore(*agentStorePath, templatePath, buildVersion)
	if err != nil {
		return err
	}
	retrievalPath := distributionStore.PayloadRetrievalPath()
	flagSet := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "payload-retrieval-path" {
			flagSet = true
		}
	})
	if flagSet || retrievalPath == "" {
		retrievalPath = *payloadRetrievalPath
		if _, err := distributionStore.SetPayloadRetrievalPath(retrievalPath); err != nil {
			return err
		}
	}
	if !validRetrievalPath(retrievalPath) {
		return errors.New("saved payload retrieval path is invalid; set --payload-retrieval-path to replace it")
	}
	distribution := &agentDistribution{store: distributionStore, manager: manager, authMode: *authMode, credential: token, retrievalPath: retrievalPath, deployment: carrier.profile}
	manager.SetAgentDistributionHandler(distribution)
	manager.SetDeploymentMethodExecutor(&windowsDeploymentExecutor{manager: manager, distribution: distribution})
	manager.SetDeploymentArtifactLookup(func(id string) (control.DeploymentArtifact, error) {
		a, err := distributionStore.Artifact(id)
		if err != nil {
			return control.DeploymentArtifact{}, err
		}
		return control.DeploymentArtifact{ID: a.ID, ProfileID: a.ProfileID, Profile: a.Profile, Platform: a.Platform, SHA256: a.SHA256, UndertowVersion: a.UndertowVersion, Revoked: a.Revoked, ServiceCapable: a.ServiceCapable}, nil
	})
	manager.SetArtifactLookup(func(id string) (string, string, bool) {
		a, err := distributionStore.EnrollmentArtifact(id)
		if err != nil || a.ID != id {
			return "", "", false
		}
		return a.Profile, a.UndertowVersion, distributionStore.ArtifactEnrollmentScoped(id)
	})
	controlToken, err := control.LoadOrCreateToken(*controlTokenPath)
	if err != nil {
		return err
	}
	ctx, stop := commandContext()
	defer stop()
	serverInfo := control.ServerInfo{Transport: *carrier.kind, Domain: *domain, WebSocketPath: *carrier.path, Fingerprint: security.Fingerprint(identity), PublicHost: publicHost}
	manager.SetServerInfo(serverInfo)
	transportManager := newServerTransports(ctx, manager, identity, token, *domain, *carrier.path, func(peer transport.Peer) {
		handleServerPeer(ctx, manager, controlToken, *probeEcho, peer)
	}, carrier.profile)
	verifyEnrollment := func(auth, transcript []byte) ([16]byte, string, error) {
		return distributionStore.VerifyEnrollment(token, auth, transcript)
	}
	transportManager.SetEnrollmentVerifier(verifyEnrollment)
	transportManager.SetArtifactHandler(http.HandlerFunc(distribution.Retrieve))
	manager.SetTransportController(transportManager)
	manager.SetRelayAcceptor(func(_ context.Context, parentID, carrier, bind string, stream *mux.Stream) {
		peer, err := relay.AcceptWithVerifier(ctx, stream, parentID, identity, token, verifyEnrollment)
		if err != nil {
			log.Printf("relay via %s rejected: %v", parentID, err)
			_ = stream.Close()
			return
		}
		peer.Carrier = carrier
		peer.RelayBind = bind
		handleServerPeer(ctx, manager, controlToken, *probeEcho, peer)
	})
	manager.SetRelayPayloadAcceptor(distribution.serveRelayPayload)
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
	transportManager.Close()
	manager.ShutdownAgentSessions()
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
	if err := carrier.load(f); err != nil {
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
	config := agentruntime.Config{Version: agentruntime.ConfigVersion, Server: *server, Transport: *carrier.kind,
		Domain: *domain, Fingerprint: pinnedFingerprint, AuthMode: *authMode, Credential: token,
		PayloadProfile: *profileFlag, WebSocketPath: *carrier.path, TLSServerName: *carrier.serverName,
		TLSInsecureSkipVerify: *carrier.skipTLSVerify, AdvertisedRoutes: advertise,
		DeniedCapabilities: *deny, IdentityPath: *keyPath, Deployment: carrier.profile}
	ready := func() error {
		if savePin {
			if err := saveServerFingerprint(*fingerprintFile, pinnedFingerprint); err != nil {
				return fmt.Errorf("save server fingerprint: %w", err)
			}
			log.Printf("trusted server fingerprint saved to %s: %s", *fingerprintFile, pinnedFingerprint)
			savePin = false
		}
		return markBackgroundReady()
	}
	if *probe || *probeCount > 0 {
		key, err := security.LoadOrCreateKey(*keyPath)
		if err != nil {
			return err
		}
		for {
			c, err := config.Dial(ctx, key)
			if err == nil {
				if err := ready(); err != nil {
					c.Close()
					return err
				}
				err = runProbes(ctx, c, *probeSize, *probeCount, *interval)
				c.Close()
				if *probeCount > 0 && err == nil {
					return nil
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("probe reconnect after %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(deployment.Jitter(carrier.profile.Reconnect.ManualDelay.Value(), carrier.profile.Reconnect.JitterPercent)):
			}
		}
	}
	return agentruntime.Run(ctx, config, ready)
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
