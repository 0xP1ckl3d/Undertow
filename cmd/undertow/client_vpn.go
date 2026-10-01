//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/netstack"
	"undertow/internal/pivot"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/tun"
)

func clientCommand(args []string) error {
	if len(args) > 0 && (args[0] == "attach" || args[0] == "console") {
		return attachClient(args[1:])
	}
	f := flag.NewFlagSet("client", flag.ContinueOnError)
	lifecycle := addLifecycleFlags(f, "client")
	carrier := addCarrierFlags(f)
	vpn := f.Bool("vpn", false, "route IPv4 traffic through the privileged VPN client")
	internal := f.Bool("internal", false, "use configured agent routes without changing the Internet route")
	server := f.String("server", "", "server host:port (numeric IPv4 for DNS)")
	domain := f.String("domain", "t.undertow.invalid", "synthetic DNS domain")
	fingerprint := f.String("fingerprint", "", "pinned server identity fingerprint")
	fingerprintFile := f.String("fingerprint-file", "server.fingerprint", "saved server fingerprint")
	trustFirstUse := f.Bool("trust-on-first-use", false, "save server fingerprint after first authenticated connection")
	tokenPath := f.String("token-file", "", "enrolment token file (default token.key)")
	tokenValue := f.String("token", "", "enrolment token hex; visible in process listings")
	authMode := f.String("auth", "token", "client enrollment: token, password, none")
	password := f.String("password", "", "shared enrollment password; visible in process listings")
	passwordFile := f.String("password-file", "", "read shared enrollment password from a file")
	keyPath := f.String("client-key", "client.key", "client identity key file")
	tunName := f.String("tun-name", "undertow-vpn", "VPN TUN/Wintun adapter name")
	address := f.String("tunnel-address", "172.16.253.1/24", "client TUN IPv4 address/prefix")
	profileFlag := f.String("payload-profile", "auto", "DNS payload profile: auto, large, small")
	verifyURL := f.String("verify-url", "https://api.ipify.org", "public IPv4 verification endpoint")
	interactive := f.Bool("interactive", false, "open a command console while the VPN runs")
	routesFile := f.String("routes-file", "client-routes.json", "persist this client's accepted agent routes")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected client argument %q; use 'undertow client --vpn or --internal ...'", f.Arg(0))
	}
	if !*lifecycle.stop && !*vpn && !*internal {
		return errors.New("client requires at least one of --vpn or --internal")
	}
	if *interactive && (*lifecycle.background || *lifecycle.foreground || *lifecycle.stop) {
		return errors.New("--interactive cannot be combined with --foreground, --background, or --stop")
	}
	if !*lifecycle.background && !*lifecycle.foreground && !*lifecycle.stop && os.Getenv(backgroundModeEnv) != "client" && (*interactive || isConsoleTerminal(os.Stdin)) {
		logPath, err := filepath.Abs(*lifecycle.logFile)
		if err != nil {
			return err
		}
		pidPath, err := filepath.Abs(*lifecycle.pidFile)
		if err != nil {
			return err
		}
		childArgs := make([]string, 0, len(args))
		for _, arg := range args {
			if arg != "--interactive" && arg != "-interactive" {
				childArgs = append(childArgs, arg)
			}
		}
		if err := launchBackground("client", childArgs, logPath, pidPath); err != nil {
			return err
		}
		return attachClientAt(pidPath)
	}
	handled, cleanup, err := lifecycle.handle(args)
	if err != nil || handled {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	} else {
		pidPath, err := filepath.Abs(*lifecycle.pidFile)
		if err != nil {
			return err
		}
		cleanup, err = startBackgroundControl(pidPath)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	if *server == "" {
		return errors.New("--server is required")
	}
	if *profileFlag != "auto" && *profileFlag != "large" && *profileFlag != "small" {
		return errors.New("invalid --payload-profile")
	}
	if *carrier.kind != "dns" && *profileFlag != "auto" {
		return errors.New("--payload-profile applies only to DNS transport")
	}
	host, _, err := net.SplitHostPort(*server)
	if err != nil {
		return err
	}
	serverIP, err := netip.ParseAddr(host)
	if *carrier.kind == "dns" && (err != nil || !serverIP.Is4()) {
		return errors.New("DNS --server must contain an IPv4 address")
	}
	if *carrier.kind != "dns" && (err != nil || !serverIP.Is4()) {
		serverIP = netip.Addr{}
	}
	prefix, err := netip.ParsePrefix(*address)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("--tunnel-address must be IPv4 CIDR")
	}
	if prefix.Contains(serverIP) {
		return errors.New("VPN tunnel network contains the server address")
	}
	networks, err := tun.ExistingNetworks()
	if err != nil {
		return err
	}
	for _, existing := range networks {
		if existing.Contains(prefix.Masked().Addr()) || prefix.Masked().Contains(existing.Addr()) {
			return fmt.Errorf("VPN network %s conflicts with local network %s", prefix.Masked(), existing)
		}
	}
	ctx, stop := commandContext()
	defer stop()
	if *interactive {
		logFile, err := os.OpenFile(*lifecycle.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("open interactive log: %w", err)
		}
		log.SetOutput(logFile)
		defer func() { log.SetOutput(os.Stderr); _ = logFile.Close() }()
	}
	savedRoutes, err := loadClientRoutes(*routesFile)
	if err != nil {
		return err
	}
	live := &liveClientConsole{routeFile: *routesFile, routes: savedRoutes, serverIP: serverIP, tunnelPrefix: prefix.Masked(), localNetworks: networks, vpn: *vpn, events: make(chan string, 16)}
	setBackgroundConsoleHandler(func(ctx context.Context, request consoleRPCRequest) consoleRPCResponse {
		var response consoleRPCResponse
		switch request.Action {
		case "session":
			response.SessionID = live.id()
		case "call":
			data, err := live.call(ctx, request.Method, request.Path, request.Body)
			response.Data = data
			if err != nil {
				response.Error = err.Error()
			}
		case "routes":
			var output bytes.Buffer
			if err := live.routeCommand(ctx, request.Args, &output); err != nil {
				response.Error = err.Error()
			}
			response.Output = output.String()
		case "transfer":
			result, err := live.transfer(ctx, request.Body)
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Data = result
			}
		default:
			response.Error = "unknown client console command"
		}
		return response
	})
	setBackgroundInteractiveHandler(func(ctx context.Context, agentID string, conn net.Conn) error {
		live.mu.RLock()
		session := live.session
		live.mu.RUnlock()
		return control.BridgeClientInteractive(ctx, session, agentID, conn)
	})
	setBackgroundScriptHandler(func(ctx context.Context, agentID string, conn net.Conn) error {
		live.mu.RLock()
		session := live.session
		live.mu.RUnlock()
		return control.BridgeClientScript(ctx, session, agentID, conn)
	})
	setBackgroundWASMHandler(func(ctx context.Context, agentID string, conn net.Conn) error {
		live.mu.RLock()
		session := live.session
		live.mu.RUnlock()
		return control.BridgeClientWASM(ctx, session, agentID, conn)
	})
	setBackgroundTransferHandler(func(ctx context.Context, encoded json.RawMessage, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		return live.transferProgress(ctx, encoded, progress)
	})
	if *interactive {
		opener := func(ctx context.Context, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
			live.mu.RLock()
			session := live.session
			live.mu.RUnlock()
			return control.OpenClientInteractive(ctx, session, agentID, request)
		}
		script := func(ctx context.Context, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
			live.mu.RLock()
			session := live.session
			live.mu.RUnlock()
			return control.OpenClientScript(ctx, session, agentID, language, source)
		}
		wasm := func(ctx context.Context, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
			live.mu.RLock()
			session := live.session
			live.mu.RUnlock()
			return control.OpenClientWASM(ctx, session, agentID, module, args, stdin)
		}
		transfer := func(ctx context.Context, request clientFileRequest, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
			encoded, err := json.Marshal(request)
			if err != nil {
				return pivot.FileMessage{}, err
			}
			return live.transferProgress(ctx, encoded, progress)
		}
		go func() {
			if err := runConsole(ctx, os.Stdin, os.Stdout, live.call, live.id, stop, live.routeCommand, live.events, consoleFeatures{open: opener, script: script, wasm: wasm, transfer: transfer}); err != nil && ctx.Err() == nil {
				log.Printf("console: %v", err)
				live.notify("Console failed: " + err.Error())
				stop()
			}
		}()
	}
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
	for ctx.Err() == nil {
		c, err := carrier.dial(ctx, *server, *domain, pinnedFingerprint, token, key, *profileFlag)
		if err != nil {
			if tlsErr := clientTLSVerificationError(err, *carrier.kind, *carrier.skipTLSVerify, *server); tlsErr != nil {
				return tlsErr
			}
		}
		if err == nil {
			carrierIP := serverIP
			if remote, ok := c.(interface{ RemoteAddr() string }); ok {
				if host, _, splitErr := net.SplitHostPort(remote.RemoteAddr()); splitErr == nil {
					if peerIP, parseErr := netip.ParseAddr(host); parseErr == nil && peerIP.Is4() {
						carrierIP = peerIP
					}
				}
			}
			if !carrierIP.Is4() {
				c.Close()
				return errors.New("VPN client requires an IPv4 carrier endpoint")
			}
			if prefix.Contains(carrierIP) {
				c.Close()
				return errors.New("VPN tunnel network contains the carrier endpoint address")
			}
			live.routeMu.Lock()
			if !serverIP.IsValid() {
				live.serverIP = carrierIP
			}
			live.carrierIP = carrierIP
			live.routeMu.Unlock()
			if savePin {
				if err := saveServerFingerprint(*fingerprintFile, pinnedFingerprint); err != nil {
					c.Close()
					return fmt.Errorf("save server fingerprint: %w", err)
				}
				log.Printf("trusted server fingerprint saved to %s: %s", *fingerprintFile, pinnedFingerprint)
				savePin = false
			}
			err = runVPN(ctx, c, carrierIP, serverIP, *vpn, *internal, *tunName, *address, prefix, *verifyURL, live.set)
			c.Close()
			if err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
				live.notify("VPN error: " + err.Error())
				return err
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("VPN session ended: %v", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

// Certificate failures need operator action. Retrying them silently can leave
// the attached console waiting until the background startup timeout expires.
func clientTLSVerificationError(err error, kind string, skipVerify bool, server string) error {
	if err == nil || skipVerify || (kind != "quic" && kind != "websocket") {
		return nil
	}
	message := err.Error()
	if !strings.Contains(message, "tls: failed to verify certificate") && !strings.Contains(message, "x509:") {
		return nil
	}
	return fmt.Errorf("TLS certificate verification failed for %s; if the server uses a self-signed certificate, add --tls-insecure-skip-verify (the Undertow --fingerprint is still checked): %w", server, err)
}

type liveClientConsole struct {
	mu            sync.RWMutex
	routeMu       sync.Mutex
	session       *mux.Mux
	sessionID     uint64
	device        clientRouteDevice
	active        map[string]bool
	global        map[string]bool
	vpn           bool
	localNetworks []netip.Prefix
	routeFile     string
	routes        []control.AcceptedRoute
	events        chan string
	serverIP      netip.Addr
	carrierIP     netip.Addr
	tunnelPrefix  netip.Prefix
}

func (c *liveClientConsole) notify(message string) {
	select {
	case c.events <- message:
	default:
	}
}

func (c *liveClientConsole) id() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func (c *liveClientConsole) call(ctx context.Context, method, path string, body any) ([]byte, error) {
	if path == "/v1/file/transfer" {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return c.transfer(ctx, encoded)
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	return control.CallRemote(ctx, session, method, path, body)
}

func (c *liveClientConsole) transfer(ctx context.Context, encoded []byte) ([]byte, error) {
	result, err := c.transferProgress(ctx, encoded, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (c *liveClientConsole) transferProgress(ctx context.Context, encoded []byte, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
	var input clientFileRequest
	if err := json.Unmarshal(encoded, &input); err != nil {
		return pivot.FileMessage{}, errors.New("invalid file transfer request")
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	return pivot.TransferFileProgress(ctx, session, input.AgentID, input.Operation, input.LocalPath, input.RemotePath, progress)
}

func runVPN(parent context.Context, c transport.Connection, carrierIP, serverIP netip.Addr, vpn, internal bool, name, address string, prefix netip.Prefix, verifyURL string, onActive func(*mux.Mux, uint64, *tun.Device)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m := mux.New(ctx, c, false)
	defer m.Close()
	go pivot.ServeClientForwards(ctx, m)
	hostname, _ := os.Hostname()
	hello, _ := json.Marshal(struct {
		Mode     string `json:"mode"`
		Internal bool   `json:"internal"`
		Hostname string `json:"hostname"`
	}{"vpn", internal, hostname})
	if err := m.SendControl(ctx, hello); err != nil {
		return err
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 15*time.Second)
	reply, err := m.RecvControl(readyCtx)
	readyCancel()
	if err != nil {
		return fmt.Errorf("VPN health handshake: %w", err)
	}
	var status struct {
		Mode  string `json:"mode"`
		Ready bool   `json:"ready"`
	}
	if json.Unmarshal(reply, &status) != nil || status.Mode != "vpn" || !status.Ready {
		return errors.New("VPN server did not confirm readiness")
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 10*time.Second)
	probe, err := m.Open(probeCtx, "health.undertow.invalid:0")
	probeCancel()
	if err != nil {
		return fmt.Errorf("VPN stream health check: %w", err)
	}
	_ = probe.Close()
	var verificationAddress string
	if vpn && verifyURL != "" {
		resolveCtx, resolveCancel := context.WithTimeout(ctx, 10*time.Second)
		verificationAddress, err = resolveVerificationTarget(resolveCtx, verifyURL)
		resolveCancel()
		if err != nil {
			return fmt.Errorf("public egress target: %w", err)
		}
		egressCtx, egressCancel := context.WithTimeout(ctx, 15*time.Second)
		egressProbe, probeErr := m.Open(egressCtx, verificationAddress)
		egressCancel()
		if probeErr != nil {
			return fmt.Errorf("server cannot reach %s before VPN routing: %w", verificationAddress, probeErr)
		}
		_ = egressProbe.Close()
		log.Printf("server egress TCP preflight succeeded: %s", verificationAddress)
	}
	device, err := tun.Open(name, address)
	if err != nil {
		return err
	}
	stackErr := make(chan error, 1)
	stackDone := make(chan struct{})
	go func() {
		defer close(stackDone)
		stackErr <- netstack.ServeEgress(ctx, device, prefix, func(netip.Addr) (*mux.Mux, bool) { return m, true })
	}()
	defer func() {
		cancel()
		_ = device.Close()
		select {
		case <-stackDone:
		case <-time.After(2 * time.Second):
			log.Print("VPN packet stack did not stop before reconnect")
		}
	}()
	unpin, err := tun.PinServer(carrierIP)
	if err != nil {
		return fmt.Errorf("pin carrier route: %w", err)
	}
	defer unpin()
	if vpn && carrierIP.IsLoopback() {
		// A proxy on this machine also opens its own socket to the server.
		// Keep that upstream connection outside the VPN's /1 defaults.
		if !serverIP.Is4() {
			return errors.New("VPN through a local proxy requires a numeric IPv4 --server address")
		}
		unpinServer, err := tun.PinServer(serverIP)
		if err != nil {
			return fmt.Errorf("pin local proxy upstream route: %w", err)
		}
		defer unpinServer()
	}
	removeModeRoutes, err := installClientModeRoutes(device, vpn)
	if err != nil {
		return err
	}
	defer removeModeRoutes()
	if vpn && verifyURL != "" {
		log.Printf("VPN routes installed through %s; verifying egress", carrierIP)
		verifyCtx, verifyCancel := context.WithTimeout(ctx, 30*time.Second)
		type verificationResult struct {
			ip  string
			err error
		}
		verified := make(chan verificationResult, 1)
		go func() {
			ip, err := verifyPublicIP(verifyCtx, verifyURL, verificationAddress)
			verified <- verificationResult{ip, err}
		}()
		var publicIP string
		var verifyErr error
		select {
		case result := <-verified:
			publicIP, verifyErr = result.ip, result.err
		case stackFailure := <-stackErr:
			verifyCancel()
			return fmt.Errorf("VPN packet stack stopped during verification: %v", stackFailure)
		case <-ctx.Done():
			verifyCancel()
			return ctx.Err()
		}
		verifyCancel()
		if verifyErr != nil {
			return fmt.Errorf("public egress verification failed: %w", verifyErr)
		}
		log.Printf("public egress IP: %s", publicIP)
	}
	if vpn {
		log.Printf("VPN active through %s; internal pivots=%t", carrierIP, internal)
	} else {
		log.Printf("internal tunnel active through %s; Internet routes unchanged", carrierIP)
	}
	if err := markBackgroundReady(); err != nil {
		return err
	}
	if onActive != nil {
		onActive(m, c.ID(), device)
		defer onActive(nil, 0, nil)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-m.Done():
		return io.EOF
	case err := <-stackErr:
		return err
	}
}

func installClientModeRoutes(device clientRouteDevice, vpn bool) (func(), error) {
	if !vpn {
		return func() {}, nil
	}
	routes := []string{"0.0.0.0/1", "128.0.0.0/1"}
	installed := make([]string, 0, len(routes))
	cleanup := func() {
		for i := len(installed) - 1; i >= 0; i-- {
			_ = device.DelRoute(installed[i])
		}
	}
	for _, route := range routes {
		if err := device.AddRoute(route); err != nil {
			cleanup()
			return nil, fmt.Errorf("install VPN route %s: %w", route, err)
		}
		installed = append(installed, route)
	}
	return cleanup, nil
}

func resolveVerificationTarget(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("verification URL must be HTTP(S) with a host")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", u.Hostname())
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", u.Hostname(), err)
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("no IPv4 address for %s", u.Hostname())
	}
	return net.JoinHostPort(addresses[0].Unmap().String(), port), nil
}

func verifyPublicIP(ctx context.Context, rawURL, fixedAddress string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", fixedAddress)
	}}}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 128))
	if err != nil {
		return "", err
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(raw)))
	if err != nil || !ip.Is4() {
		return "", errors.New("verification endpoint did not return an IPv4 address")
	}
	return ip.String(), nil
}
