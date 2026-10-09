package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"undertow/internal/control"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

type operatorOptions struct {
	address, tokenFile, via string
	json                    bool
	positional              []string
}

func openControlInteractive(ctx context.Context, options operatorOptions, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "interactive")
	if err != nil {
		return nil, err
	}
	return pivot.StartInteractive(ctx, conn, reader, request)
}

func openControlScript(ctx context.Context, options operatorOptions, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "script")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Language: language, Size: len(source)}, source)
}

func openControlWASM(ctx context.Context, options operatorOptions, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "wasm")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Stdin: stdin, Size: len(module)}, module)
}

func openControlNative(ctx context.Context, options operatorOptions, agentID string, module []byte, args []string, data []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "native")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Stdin: data, Size: len(module)}, module)
}

func openControlAssembly(ctx context.Context, options operatorOptions, agentID string, source []byte, args []string) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "assembly")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Size: len(source)}, source)
}

func openControlBOF(ctx context.Context, options operatorOptions, agentID string, object, arguments []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openControlSession(ctx, options, agentID, "bof")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Size: len(object), Stdin: arguments}, object)
}

func openControlSession(ctx context.Context, options operatorOptions, agentID, operation string) (*net.TCPConn, *bufio.Reader, error) {
	token, err := os.ReadFile(options.tokenFile)
	if err != nil {
		return nil, nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", options.address)
	if err != nil {
		return nil, nil, err
	}
	path := "/v1/agents/" + url.PathEscape(agentID) + "/" + operation
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\nX-Undertow-Token-Context: %s\r\n\r\n", path, strings.TrimSpace(string(token)), pivot.TokenContextID(ctx)); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		conn.Close()
		return nil, nil, fmt.Errorf("task connection: %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return conn.(*net.TCPConn), reader, nil
}

func parseOperator(args []string) (operatorOptions, error) {
	o := operatorOptions{address: "127.0.0.1:47889", tokenFile: "control.key"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--control", "--control-token-file", "--via":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", args[i])
			}
			i++
			switch args[i-1] {
			case "--control":
				o.address = args[i]
			case "--control-token-file":
				o.tokenFile = args[i]
			case "--via":
				o.via = args[i]
			}
		case "--json":
			o.json = true
		default:
			o.positional = append(o.positional, args[i])
		}
	}
	host, _, err := net.SplitHostPort(o.address)
	if err != nil {
		return o, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return o, errors.New("operator control address must be numeric loopback")
	}
	return o, nil
}

func callControl(o operatorOptions, method, path string, body any) ([]byte, error) {
	tokenBytes, err := os.ReadFile(o.tokenFile)
	if err != nil {
		return nil, err
	}
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+o.address+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokenBytes)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	timeout := 10 * time.Second
	if control.IsForegroundRequest(method, path) {
		timeout = 40 * time.Hour
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("control API: %s: %s", resp.Status, strings.TrimSpace(string(result)))
	}
	return result, nil
}

func printJSON(data []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return err
	}
	fmt.Println(pretty.String())
	return nil
}

func statusCommand(args []string) error {
	o, err := parseOperator(args)
	if err != nil {
		return err
	}
	if len(o.positional) != 0 {
		return errors.New("usage: undertow status [--json]")
	}
	data, err := callControl(o, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return err
	}
	if o.json {
		return printJSON(data)
	}
	return renderStatus(os.Stdout, data)
}

func renderStatus(w io.Writer, data []byte) error {
	var status struct {
		Server   control.ServerInfo   `json:"server"`
		Agents   []control.AgentInfo  `json:"agents"`
		Clients  []control.ClientInfo `json:"clients"`
		Routes   []routing.Route      `json:"routes"`
		Selected string               `json:"selected_agent"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	if len(status.Server.Listeners) != 0 {
		fmt.Fprintln(w, "Server listeners:")
		fmt.Fprintf(w, "  %-12s %-8s %-22s %-14s %8s\n", "Transport", "Network", "Listen", "TLS", "Sessions")
		for _, listener := range status.Server.Listeners {
			tlsMode := listener.TLSMode
			if tlsMode == "" {
				tlsMode = "-"
			}
			fmt.Fprintf(w, "  %-12s %-8s %-22s %-14s %8d\n", strings.ToUpper(listener.Transport), strings.ToUpper(listener.Network), listener.Listen, tlsMode, listener.Sessions)
		}
		if status.Server.Fingerprint != "" {
			fmt.Fprintf(w, "Fingerprint: %s\n", status.Server.Fingerprint)
		}
		fmt.Fprintln(w)
	} else if status.Server.Transport != "" {
		fmt.Fprintf(w, "Server: %s (%s) listening on %s\n", status.Server.Transport, status.Server.Network, status.Server.Listen)
		if status.Server.Domain != "" {
			fmt.Fprintf(w, "DNS domain: %s\n", status.Server.Domain)
		}
		if status.Server.WebSocketPath != "" {
			fmt.Fprintf(w, "WebSocket path: %s\n", status.Server.WebSocketPath)
		}
		if status.Server.TLSMode != "" {
			fmt.Fprintf(w, "TLS: %s\n", status.Server.TLSMode)
		}
		if status.Server.Fingerprint != "" {
			fmt.Fprintf(w, "Fingerprint: %s\n", status.Server.Fingerprint)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Agents (%d)\n", len(status.Agents))
	fmt.Fprintf(w, "%-18s %-16s %-12s %-24s %-18s %8s %8s %7s %4s %4s\n", "ID", "Virtual IP", "Transport", "Host", "Remote", "RX", "TX", "Streams", "Jobs", "Fwd")
	for _, a := range status.Agents {
		id := a.ID
		if len(id) > 18 {
			id = id[:18]
		}
		fmt.Fprintf(w, "%-18s %-16s %-12s %-24s %-18s %8d %8d %7d %4d %4d\n", id, a.VirtualIP, a.Transport, a.Hostname, a.Remote, a.RXBytes, a.TXBytes, a.Streams, a.ActiveJobs, a.ActiveForwards)
		if a.Capabilities != nil {
			fmt.Fprintf(w, "  capabilities supported=%s allowed=%s\n", strings.Join(a.Capabilities.Supported, ","), strings.Join(a.Capabilities.Allowed, ","))
		} else {
			fmt.Fprintln(w, "  capabilities unknown (older agent)")
		}
	}
	fmt.Fprintf(w, "\nVPN clients (%d)\n", len(status.Clients))
	fmt.Fprintf(w, "%-20s %-24s %-12s %-18s %-8s %8s %8s %7s %6s %6s %6s %8s\n", "Session", "Host", "Transport", "Remote", "Internal", "RX", "TX", "Streams", "Queued", "Flight", "CWND", "Retrans")
	for _, c := range status.Clients {
		fmt.Fprintf(w, "%-20d %-24s %-12s %-18s %-8t %8d %8d %7d %6d %6d %6d %8d\n", c.SessionID, c.Hostname, c.Transport, c.Remote, c.Internal, c.RXBytes, c.TXBytes, c.Streams, c.Queued, c.InFlight, c.Window, c.Retransmits)
		for _, route := range c.AcceptedRoutes {
			fmt.Fprintf(w, "  accepted %s via %s\n", route.Prefix, agentRouteLabel(route.AgentID, status.Agents))
		}
	}
	fmt.Fprintf(w, "\nRoutes (%d)\n", len(status.Routes))
	for _, r := range status.Routes {
		fmt.Fprintf(w, "%s via %s active=%v\n", r.Prefix, agentRouteLabel(r.AgentID, status.Agents), r.Active)
	}
	if status.Selected != "" {
		fmt.Fprintf(w, "Selected: %s\n", status.Selected)
	}
	return nil
}

func renderAgentShow(w io.Writer, a control.AgentInfo) error {
	fmt.Fprintf(w, "Agent ID: %s\n", a.ID)
	fmt.Fprintf(w, "Host: %s  OS: %s/%s  Virtual IP: %s  Remote: %s  Transport: %s\n", a.Hostname, a.OS, a.Arch, a.VirtualIP, a.Remote, a.Transport)
	if a.ArtifactID != "" {
		fmt.Fprintf(w, "Profile name: %s  Profile ID: %s\nPayload ID: %s (use with payload show)  Undertow: %s\n", a.Profile, a.ProfileID, a.ArtifactID, a.UndertowVersion)
	}
	if !a.Connected.IsZero() {
		fmt.Fprintf(w, "Connected: %s  Duration: %s\n", a.Connected.Local().Format(time.RFC3339), time.Since(a.Connected).Round(time.Second))
	}
	if a.ReconnectPolicy != "" {
		fmt.Fprintf(w, "Reconnect policy: %s  Attempts before session: %d\n", a.ReconnectPolicy, a.ReconnectAttempts)
	}
	if a.SleepSupported {
		fmt.Fprintf(w, "Idle sleep: %d seconds, %d%% jitter (zero disables sleep)\n", a.Sleep.IntervalSeconds, a.Sleep.JitterPercent)
	} else {
		fmt.Fprintln(w, "Idle sleep: unavailable in this agent build")
	}
	if !a.LastSeen.IsZero() {
		fmt.Fprintf(w, "Last seen: %s ago\n", time.Since(a.LastSeen).Round(time.Second))
	}
	fmt.Fprintf(w, "RTT: %s  Encrypted RX/TX: %d/%d bytes  Current RX/TX: %.1f/%.1f KiB/s\n", a.RTT, a.RXBytes, a.TXBytes, a.RXRate/1024, a.TXRate/1024)
	fmt.Fprintf(w, "Retransmits: %d  Duplicates: %d  CWND: %d  Queued: %d  In flight: %d\n", a.Retransmits, a.Duplicates, a.Window, a.Queued, a.InFlight)
	fmt.Fprintf(w, "Receive window: %d  Peer receive window: %d  DNS fragment: %d bytes  Payload adjustments: %d\n", a.ReceiveWindow, a.PeerReceiveWindow, a.FragmentSize, a.PayloadAdjustments)
	fmt.Fprintf(w, "Mux streams: %d  Active jobs: %d  Active TCP forwards: %d\n", a.Streams, a.ActiveJobs, a.ActiveForwards)
	if a.Capabilities != nil {
		fmt.Fprintf(w, "Capabilities: %s\n", strings.Join(a.Capabilities.Allowed, ", "))
	} else {
		fmt.Fprintln(w, "Capabilities: unknown (older agent)")
	}
	if len(a.Interfaces) > 0 {
		fmt.Fprintln(w, "Interfaces:")
		for _, iface := range a.Interfaces {
			fmt.Fprintf(w, "  %s\n", iface)
		}
	}
	if len(a.AdvertisedRoutes) > 0 {
		fmt.Fprintln(w, "Advertised networks:")
		for _, prefix := range a.AdvertisedRoutes {
			fmt.Fprintf(w, "  %s\n", prefix)
		}
	}
	if len(a.Routes) > 0 {
		fmt.Fprintln(w, "Discovered IPv4 routes:")
		for _, route := range a.Routes {
			kind := "direct"
			if !route.Direct {
				kind = "via " + route.Gateway
			}
			fmt.Fprintf(w, "  %-18s %-22s interface=%s type=%s source=%s\n", route.Prefix, kind, route.Interface, route.Type, route.Source)
		}
	}
	if a.DefaultRoute != nil {
		fmt.Fprintf(w, "Default route: via %s interface=%s source=%s\n", a.DefaultRoute.Gateway, a.DefaultRoute.Interface, a.DefaultRoute.Source)
	}
	if len(a.Forwards) > 0 {
		fmt.Fprintln(w, "TCP forwards:")
		for _, forward := range a.Forwards {
			fmt.Fprintf(w, "  %s -> %s (id %s)\n", forward.Bind, forward.Target, forward.ID)
		}
	}
	return nil
}

func agentCommand(args []string) error {
	o, err := parseOperator(args)
	if err != nil {
		return err
	}
	if len(o.positional) == 0 {
		return errors.New("usage: undertow agent list|show|select")
	}
	switch o.positional[0] {
	case "list":
		if len(o.positional) != 1 {
			return errors.New("usage: undertow agent list")
		}
		return statusCommand(args[1:])
	case "show":
		if len(o.positional) != 2 {
			return errors.New("usage: undertow agent show ID")
		}
		data, err := callControl(o, http.MethodGet, "/v1/agents/"+url.PathEscape(o.positional[1]), nil)
		if err != nil {
			return err
		}
		if o.json {
			return printJSON(data)
		}
		var agent control.AgentInfo
		if err := json.Unmarshal(data, &agent); err != nil {
			return err
		}
		return renderAgentShow(os.Stdout, agent)
	case "select":
		if len(o.positional) != 2 {
			return errors.New("usage: undertow agent select ID")
		}
		_, err := callControl(o, http.MethodPost, "/v1/selection", map[string]string{"agent_id": o.positional[1]})
		return err
	default:
		return errors.New("usage: undertow agent list|show|select")
	}
}

func routeCommand(args []string) error {
	o, err := parseOperator(args)
	if err != nil {
		return err
	}
	if len(o.positional) == 0 {
		return errors.New("usage: undertow route add|del|list")
	}
	switch o.positional[0] {
	case "list":
		data, err := callControl(o, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		var status struct {
			Routes []routing.Route     `json:"routes"`
			Agents []control.AgentInfo `json:"agents"`
		}
		if err = json.Unmarshal(data, &status); err != nil {
			return err
		}
		if o.json {
			b, _ := json.Marshal(status.Routes)
			return printJSON(b)
		}
		for _, r := range status.Routes {
			fmt.Printf("%s via %s active=%v\n", r.Prefix, agentRouteLabel(r.AgentID, status.Agents), r.Active)
		}
		return nil
	case "add":
		if len(o.positional) != 2 {
			return errors.New("usage: undertow route add CIDR [--via AGENT_ID]")
		}
		_, err := callControl(o, http.MethodPost, "/v1/routes", map[string]string{"prefix": o.positional[1], "agent_id": o.via})
		return err
	case "del":
		if len(o.positional) != 2 {
			return errors.New("usage: undertow route del CIDR")
		}
		_, err := callControl(o, http.MethodDelete, "/v1/routes?prefix="+url.QueryEscape(o.positional[1]), nil)
		return err
	default:
		return errors.New("usage: undertow route add|del|list")
	}
}

func sessionCommand(args []string) error {
	o, err := parseOperator(args)
	if err != nil {
		return err
	}
	if len(o.positional) != 2 || o.positional[0] != "kill" {
		return errors.New("usage: undertow session kill AGENT_ID")
	}
	_, err = callControl(o, http.MethodPost, "/v1/sessions/"+url.PathEscape(o.positional[1])+"/kill", nil)
	return err
}
