//go:build linux || windows

package main

import (
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
	"strings"
	"sync"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/netstack"
	"undertow/internal/security"
	"undertow/internal/transport/dns"
	"undertow/internal/tun"
)

func clientCommand(args []string) error {
	f := flag.NewFlagSet("client", flag.ContinueOnError)
	lifecycle := addLifecycleFlags(f, "client")
	vpn := f.Bool("vpn", false, "route IPv4 traffic through the privileged VPN client")
	internal := f.Bool("internal", false, "also honor configured agent pivot routes")
	server := f.String("server", "", "direct DNS server IPv4:port")
	domain := f.String("domain", "t.undertow.invalid", "synthetic DNS domain")
	fingerprint := f.String("fingerprint", "", "pinned server identity fingerprint")
	fingerprintFile := f.String("fingerprint-file", "server.fingerprint", "saved server fingerprint")
	trustFirstUse := f.Bool("trust-on-first-use", false, "save server fingerprint after first authenticated connection")
	tokenPath := f.String("token-file", "token.key", "enrolment token file")
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
		return fmt.Errorf("unexpected client argument %q; use 'undertow client --vpn ...'", f.Arg(0))
	}
	if *interactive && (*lifecycle.background || *lifecycle.stop) {
		return errors.New("--interactive requires a foreground VPN client")
	}
	handled, cleanup, err := lifecycle.handle(args)
	if err != nil || handled {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if !*vpn {
		return errors.New("client currently requires --vpn")
	}
	if *server == "" {
		return errors.New("--server is required")
	}
	if *profileFlag != "auto" && *profileFlag != "large" && *profileFlag != "small" {
		return errors.New("invalid --payload-profile")
	}
	host, _, err := net.SplitHostPort(*server)
	if err != nil {
		return err
	}
	serverIP, err := netip.ParseAddr(host)
	if err != nil || !serverIP.Is4() {
		return errors.New("--server must contain an IPv4 address")
	}
	prefix, err := netip.ParsePrefix(*address)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("--tunnel-address must be IPv4 CIDR")
	}
	if prefix.Contains(serverIP) {
		return errors.New("VPN tunnel network contains the DNS server address")
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
	savedRoutes, err := loadClientRoutes(*routesFile)
	if err != nil {
		return err
	}
	live := &liveClientConsole{routeFile: *routesFile, routes: savedRoutes, serverIP: serverIP, tunnelPrefix: prefix.Masked()}
	if *interactive {
		go func() {
			if err := runConsole(ctx, os.Stdin, os.Stdout, live.call, live.id, stop, live.routeCommand); err != nil && ctx.Err() == nil {
				log.Printf("console: %v", err)
				stop()
			}
		}()
	}
	pinnedFingerprint, savePin, err := resolveServerFingerprint(ctx, *server, *domain, *fingerprint, *fingerprintFile, *trustFirstUse)
	if err != nil {
		return err
	}
	token, err := security.EnrollmentSecret(*authMode, *tokenPath, *password, *passwordFile, pinnedFingerprint)
	if err != nil {
		return err
	}
	key, err := security.LoadOrCreateKey(*keyPath)
	if err != nil {
		return err
	}
	profile := byte(0)
	if *profileFlag == "small" {
		profile = 1
	}
	for ctx.Err() == nil {
		c, err := dns.DialProfile(ctx, *server, *domain, pinnedFingerprint, token, key, profile)
		if err == nil {
			if savePin {
				if err := saveServerFingerprint(*fingerprintFile, pinnedFingerprint); err != nil {
					c.Close()
					return fmt.Errorf("save server fingerprint: %w", err)
				}
				log.Printf("trusted server fingerprint saved to %s: %s", *fingerprintFile, pinnedFingerprint)
				savePin = false
			}
			err = runVPN(ctx, c, serverIP, *internal, *tunName, *address, prefix, *verifyURL, live.set)
			c.Close()
			if err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("VPN session ended: %v", err)
		if *profileFlag == "auto" && profile == 0 {
			profile = 1
			log.Print("switching to small DNS payload profile")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

type liveClientConsole struct {
	mu           sync.RWMutex
	routeMu      sync.Mutex
	session      *mux.Mux
	sessionID    uint64
	device       clientRouteDevice
	active       map[string]bool
	routeFile    string
	routes       []control.AcceptedRoute
	serverIP     netip.Addr
	tunnelPrefix netip.Prefix
}

func (c *liveClientConsole) id() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func (c *liveClientConsole) call(ctx context.Context, method, path string, body any) ([]byte, error) {
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	return control.CallRemote(ctx, session, method, path, body)
}

func runVPN(parent context.Context, c *dns.Client, serverIP netip.Addr, internal bool, name, address string, prefix netip.Prefix, verifyURL string, onActive func(*mux.Mux, uint64, *tun.Device)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m := mux.New(ctx, c, false)
	defer m.Close()
	hello, _ := json.Marshal(struct {
		Mode     string `json:"mode"`
		Internal bool   `json:"internal"`
	}{"vpn", internal})
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
	if verifyURL != "" {
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
	defer device.Close()
	stackErr := make(chan error, 1)
	go func() {
		stackErr <- netstack.ServeEgress(ctx, device, prefix, func(netip.Addr) (*mux.Mux, bool) { return m, true })
	}()
	unpin, err := tun.PinServer(serverIP)
	if err != nil {
		return fmt.Errorf("pin DNS carrier route: %w", err)
	}
	defer unpin()
	routes := []string{"0.0.0.0/1", "128.0.0.0/1"}
	installed := make([]string, 0, len(routes))
	defer func() {
		for i := len(installed) - 1; i >= 0; i-- {
			_ = device.DelRoute(installed[i])
		}
	}()
	for _, route := range routes {
		if err := device.AddRoute(route); err != nil {
			return fmt.Errorf("install VPN route %s: %w", route, err)
		}
		installed = append(installed, route)
	}
	log.Printf("VPN routes installed through %s; verifying egress", serverIP)
	if verifyURL != "" {
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
	log.Printf("VPN active through %s; internal pivots=%t", serverIP, internal)
	if onActive != nil {
		onActive(m, c.Session.ID(), device)
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
