package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"undertow/internal/tun"
)

type doctorReport struct {
	w     io.Writer
	fails int
}

func (r *doctorReport) pass(name, detail string) { fmt.Fprintf(r.w, "PASS %-18s %s\n", name, detail) }
func (r *doctorReport) warn(name, detail string) { fmt.Fprintf(r.w, "WARN %-18s %s\n", name, detail) }
func (r *doctorReport) fail(name, detail string) {
	r.fails++
	fmt.Fprintf(r.w, "FAIL %-18s %s\n", name, detail)
}

func doctorCommand(args []string, output io.Writer) error {
	if len(args) == 0 || (args[0] != "server" && args[0] != "agent" && args[0] != "client") {
		return errors.New("usage: undertow doctor server|agent|client [FLAGS]")
	}
	role := args[0]
	f := flag.NewFlagSet("doctor "+role, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultCarrier := "dns"
	if role == "server" {
		defaultCarrier = "dns,websocket,quic"
	}
	carrier := addCarrierFlags(f, defaultCarrier)
	auth := f.String("auth", "token", "enrollment mode")
	tokenFile := f.String("token-file", "token.key", "enrollment token")
	token := f.String("token", "", "enrollment token value")
	passwordFile := f.String("password-file", "", "enrollment password")
	password := f.String("password", "", "enrollment password value")
	pidFile := f.String("pid-file", "undertow-"+role+".pid", "background state")
	var listen, dnsListen, websocketListen, quicListen, controlListen, identity, server, fingerprint, fingerprintFile, tunnelAddress *string
	var tunEnabled, trustFirstUse *bool
	var routes advertisedRoutes
	var forwardValues forwards
	if role == "server" {
		listen = f.String("listen", "", "carrier listener")
		dnsListen = f.String("dns-listen", "", "DNS UDP listener")
		websocketListen = f.String("websocket-listen", "", "WebSocket TCP listener")
		quicListen = f.String("quic-listen", "", "QUIC UDP listener")
		controlListen = f.String("control-listen", "127.0.0.1:47889", "operator API")
		identity = f.String("identity", "identity.key", "server key")
		tunEnabled = f.Bool("tun", false, "server proxy TUN")
		tunnelAddress = f.String("tunnel-address", "172.16.254.1/24", "server TUN address")
		f.Var(&forwardValues, "forward", "local TCP forward")
	} else {
		server = f.String("server", "", "DNS server")
		fingerprint = f.String("fingerprint", "", "server pin")
		fingerprintFile = f.String("fingerprint-file", "server.fingerprint", "saved pin")
		trustFirstUse = f.Bool("trust-on-first-use", false, "first-use pin")
		if role == "client" {
			tunnelAddress = f.String("tunnel-address", "172.16.253.1/24", "client TUN address")
			f.Var(&routes, "route", "intended internal CIDR")
			f.Bool("vpn", false, "check VPN mode")
			f.Bool("internal", false, "check internal mode")
		}
	}
	if err := f.Parse(args[1:]); err != nil {
		return fmt.Errorf("doctor %s: %w", role, err)
	}
	if f.NArg() > 0 {
		return fmt.Errorf("doctor %s: unexpected argument %q", role, f.Arg(0))
	}
	if role != "server" {
		if err := carrier.validate(); err != nil {
			return err
		}
	}
	if role != "server" && *carrier.selfSigned {
		return errors.New("--tls-self-signed is a server-only flag")
	}
	r := &doctorReport{w: output}
	fmt.Fprintf(output, "Undertow doctor: %s (read-only)\n", role)
	r.pass("transport", *carrier.kind)
	if strings.Contains(*carrier.kind, "websocket") && (*carrier.path == "" || (*carrier.path)[0] != '/' || strings.ContainsAny(*carrier.path, "?#")) {
		r.fail("WebSocket path", "--websocket-path must start with / and contain no query or fragment")
	}
	if *carrier.kind != "dns" && *carrier.skipTLSVerify {
		r.warn("TLS verification", "certificate verification disabled; pin the Undertow identity with --fingerprint")
	}
	r.pass("platform", runtime.GOOS+"/"+runtime.GOARCH)
	privileged := doctorPrivileged()
	if role == "agent" {
		r.pass("privilege", "agent needs no elevation")
	} else if privileged {
		r.pass("privilege", "elevated")
	} else if role == "client" || *tunEnabled {
		r.fail("privilege", "TUN/Wintun setup needs root or Administrator; rerun elevated")
	} else {
		r.warn("privilege", "not elevated; binding ports below 1024 may need root on Linux")
	}
	checkDoctorCredential(r, *auth, *tokenFile, *token, *passwordFile, *password)
	if role == "server" {
		if (*carrier.cert == "") != (*carrier.key == "") {
			return errors.New("--tls-cert and --tls-key must be supplied together")
		}
		if *carrier.selfSigned && *carrier.cert != "" {
			return errors.New("--tls-self-signed cannot be combined with certificate files")
		}
		var kinds []string
		seen := make(map[string]bool)
		for _, item := range strings.Split(*carrier.kind, ",") {
			kind, err := canonicalTransport(item)
			if err != nil {
				return err
			}
			if !seen[kind] {
				kinds = append(kinds, kind)
				seen[kind] = true
			}
		}
		if *listen != "" && len(kinds) != 1 {
			return errors.New("--listen requires one transport; use per-transport listen flags")
		}
		if !seen["websocket"] && !seen["quic"] && (*carrier.cert != "" || *carrier.selfSigned) { return errors.New("TLS options require a WebSocket or QUIC server listener") }
		for _, option := range []struct{ kind, address string }{{"dns", *dnsListen}, {"websocket", *websocketListen}, {"quic", *quicListen}} {
			if option.address != "" && !seen[option.kind] { return fmt.Errorf("--%s-listen requires %s in --transport", option.kind, option.kind) }
			if *listen != "" && option.address != "" { return errors.New("choose --listen or a per-transport listen flag") }
		}
		checkDoctorFile(r, "identity", *identity, false, "run 'undertow init' to create the server identity")
		if *carrier.cert != "" || *carrier.key != "" {
			checkDoctorFile(r, "TLS certificate", *carrier.cert, true, "provide --tls-cert PEM")
			checkDoctorFile(r, "TLS key", *carrier.key, true, "provide --tls-key PEM")
		} else if seen["websocket"] || seen["quic"] {
			r.pass("TLS", "ephemeral self-signed certificate for WebSocket and QUIC")
		}
		for _, kind := range kinds {
			address := *listen
			if address == "" {
				switch kind {
				case "dns":
					address = *dnsListen
					if address == "" {
						address = "0.0.0.0:53"
					}
				case "websocket":
					address = *websocketListen
					if address == "" {
						address = "0.0.0.0:443"
					}
				case "quic":
					address = *quicListen
					if address == "" {
						address = "0.0.0.0:443"
					}
				}
			}
			network := "udp4"
			if kind == "websocket" {
				network = "tcp4"
			}
			checkDoctorBind(r, kind+" listener", network, address)
		}
		checkDoctorBind(r, "operator API", "tcp4", *controlListen)
		for _, value := range forwardValues {
			local, _, _ := strings.Cut(value, "=")
			checkDoctorBind(r, "TCP forward", "tcp4", local)
		}
		if *tunEnabled {
			checkDoctorTunnel(r, *tunnelAddress, netip.Addr{}, nil)
		}
	} else {
		serverIP := checkDoctorCarrierServer(r, *server, *carrier.kind)
		if role == "client" && *carrier.kind != "dns" && !serverIP.IsValid() {
			r.warn("carrier route", "hostname or proxy endpoint is resolved on connection; confirm it stays outside the client tunnel network")
		}
		if *fingerprint != "" {
			if _, err := normalizeFingerprint(*fingerprint); err != nil {
				r.fail("fingerprint", err.Error()+"; copy the value printed by 'undertow init'")
			} else {
				r.pass("fingerprint", "explicit server pin is valid")
			}
		} else if *trustFirstUse {
			r.warn("fingerprint", "trust on first use cannot verify an intercepted first contact; prefer --fingerprint")
		} else {
			checkDoctorFingerprintFile(r, *fingerprintFile)
		}
		if role == "client" {
			checkDoctorTunnel(r, *tunnelAddress, serverIP, routes)
		}
	}
	checkDoctorState(r, *pidFile)
	fmt.Fprintf(output, "Result: %d fail(s)\n", r.fails)
	if r.fails > 0 {
		return errors.New("doctor found startup blockers")
	}
	return nil
}

func checkDoctorCredential(r *doctorReport, auth, tokenFile, token, passwordFile, password string) {
	switch auth {
	case "none":
		r.warn("enrollment", "open enrollment lets anyone who reaches the server join; use token for real deployments")
	case "token":
		if token != "" {
			r.pass("enrollment", "token provided on command line")
		} else {
			checkDoctorFile(r, "enrollment", tokenFile, true, "copy token.key securely from SERVER or run 'undertow init'")
		}
	case "password":
		if password != "" {
			r.pass("enrollment", "password provided on command line")
		} else if passwordFile != "" {
			checkDoctorFile(r, "enrollment", passwordFile, true, "supply a readable --password-file")
		} else {
			r.fail("enrollment", "password mode needs --password-file or --password")
		}
	default:
		r.fail("enrollment", "choose --auth token, password, or none")
	}
}

func checkDoctorFile(r *doctorReport, name, path string, required bool, remedy string) {
	data, err := os.ReadFile(path)
	if err != nil {
		message := fmt.Sprintf("cannot read %s: %v; %s", path, err, remedy)
		if required {
			r.fail(name, message)
		} else {
			r.warn(name, message)
		}
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		r.fail(name, fmt.Sprintf("%s is empty; %s", path, remedy))
		return
	}
	r.pass(name, path+" is readable")
}

func checkDoctorFingerprintFile(r *doctorReport, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		r.fail("fingerprint", fmt.Sprintf("cannot read %s: %v; pass --fingerprint or create a saved pin", path, err))
		return
	}
	if _, err := normalizeFingerprint(string(data)); err != nil {
		r.fail("fingerprint", fmt.Sprintf("%s: %v; copy the server fingerprint from 'undertow init'", path, err))
		return
	}
	r.pass("fingerprint", path+" contains a valid pin")
}

func doctorAddress(value string) (netip.Addr, int, error) {
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() {
		return netip.Addr{}, 0, errors.New("use a numeric IPv4 address")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return netip.Addr{}, 0, errors.New("port must be 1-65535")
	}
	return ip, port, nil
}

func checkDoctorServer(r *doctorReport, value string) netip.Addr {
	ip, _, err := doctorAddress(value)
	if err != nil || ip.IsUnspecified() {
		r.fail("server address", "set --server to a reachable numeric IPv4:port, such as 203.0.113.10:53")
		return netip.Addr{}
	}
	r.pass("server address", value+" is syntactically valid")
	return ip
}

func checkDoctorCarrierServer(r *doctorReport, value, carrier string) netip.Addr {
	if carrier == "dns" {
		return checkDoctorServer(r, value)
	}
	host, rawPort, err := net.SplitHostPort(value)
	port, portErr := strconv.Atoi(rawPort)
	if err != nil || portErr != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "/\\ ") {
		r.fail("server address", "set --server to a reachable host:port, such as vpn.example.com:443")
		return netip.Addr{}
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if ip.IsUnspecified() {
			r.fail("server address", "use a reachable host or IP, not an unspecified address")
			return netip.Addr{}
		}
		r.pass("server address", value+" is syntactically valid")
		return ip
	}
	r.pass("server address", value+" is syntactically valid; DNS resolution will occur on connection")
	return netip.Addr{}
}

func checkDoctorBind(r *doctorReport, name, network, address string) {
	if _, _, err := doctorAddress(address); err != nil {
		r.fail(name, "invalid bind address "+address+": "+err.Error())
		return
	}
	var listener io.Closer
	var err error
	if network == "udp4" {
		listener, err = net.ListenPacket(network, address)
	} else {
		listener, err = net.Listen(network, address)
	}
	if err != nil {
		r.fail(name, fmt.Sprintf("cannot bind %s: %v; choose a free address/port or stop its current owner", address, err))
		return
	}
	_ = listener.Close()
	r.pass(name, address+" is available")
}

func checkDoctorTunnel(r *doctorReport, address string, serverIP netip.Addr, routes []string) {
	if err := tun.CheckAvailability(); err != nil {
		r.fail("TUN/Wintun", err.Error())
	} else {
		r.pass("TUN/Wintun", "local prerequisite is available")
	}
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() {
		r.fail("tunnel network", "use an IPv4 CIDR for --tunnel-address")
		return
	}
	prefix = prefix.Masked()
	if serverIP.IsValid() && prefix.Contains(serverIP) {
		r.fail("tunnel network", "tunnel network contains the server address; choose another prefix")
	}
	networks, err := tun.ExistingNetworks()
	if err != nil {
		r.warn("local routes", "cannot inspect interfaces/routes: "+err.Error())
		return
	}
	conflicts := false
	for _, existing := range networks {
		if doctorOverlap(prefix, existing) {
			r.fail("tunnel network", fmt.Sprintf("%s overlaps local %s; choose another --tunnel-address", prefix, existing))
			conflicts = true
		}
	}
	if !conflicts {
		r.pass("tunnel network", prefix.String()+" does not overlap detected local networks")
	}
	for _, raw := range routes {
		candidate, err := netip.ParsePrefix(raw)
		if err != nil || !candidate.Addr().Is4() {
			r.fail("internal route", raw+" is not an IPv4 CIDR")
			continue
		}
		candidate = candidate.Masked()
		if doctorOverlap(candidate, prefix) {
			r.fail("internal route", candidate.String()+" overlaps the tunnel network")
			continue
		}
		found := false
		for _, existing := range networks {
			if doctorOverlap(candidate, existing) {
				r.warn("internal route", fmt.Sprintf("%s overlaps local %s; local routing may take precedence", candidate, existing))
				found = true
				break
			}
		}
		if !found {
			r.pass("internal route", candidate.String()+" has no detected local overlap")
		}
	}
}

func doctorOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func checkDoctorState(r *doctorReport, path string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		r.pass("background state", "no state file at "+path)
		return
	}
	if err != nil {
		r.warn("background state", fmt.Sprintf("cannot read %s: %v; use the same --pid-file as startup", path, err))
		return
	}
	var state backgroundState
	if json.Unmarshal(data, &state) != nil || state.PID < 1 || state.Address == "" {
		r.warn("background state", path+" is invalid; inspect before removing a stale file")
		return
	}
	conn, err := net.DialTimeout("tcp", state.Address, 250*time.Millisecond)
	if err != nil {
		r.warn("background state", fmt.Sprintf("%s records PID %d but local control is unreachable; inspect before removing it", path, state.PID))
		return
	}
	_ = conn.Close()
	r.warn("background state", fmt.Sprintf("%s records a reachable background process (PID %d); use the matching --stop if restarting", path, state.PID))
}
