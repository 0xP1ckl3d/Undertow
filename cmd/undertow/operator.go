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
	token, err := os.ReadFile(options.tokenFile)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", options.address)
	if err != nil {
		return nil, err
	}
	path := "/v1/agents/" + url.PathEscape(agentID) + "/interactive"
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\n\r\n", path, strings.TrimSpace(string(token))); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		conn.Close()
		return nil, fmt.Errorf("interactive connection: %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return pivot.StartInteractive(ctx, conn.(*net.TCPConn), reader, request)
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
	client := &http.Client{Timeout: 10 * time.Second}
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
		Agents   []control.AgentInfo  `json:"agents"`
		Clients  []control.ClientInfo `json:"clients"`
		Routes   []routing.Route      `json:"routes"`
		Selected string               `json:"selected_agent"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	fmt.Fprintf(w, "Agents (%d)\n", len(status.Agents))
	fmt.Fprintf(w, "%-18s %-16s %-24s %-18s %8s %8s %7s %4s\n", "ID", "Virtual IP", "Host", "Remote", "RX", "TX", "Streams", "Jobs")
	for _, a := range status.Agents {
		id := a.ID
		if len(id) > 18 {
			id = id[:18]
		}
		fmt.Fprintf(w, "%-18s %-16s %-24s %-18s %8d %8d %7d %4d\n", id, a.VirtualIP, a.Hostname, a.Remote, a.RXBytes, a.TXBytes, a.Streams, a.ActiveJobs)
		for _, prefix := range a.AdvertisedRoutes {
			fmt.Fprintf(w, "  advertised %s via %s\n", prefix, a.ID)
		}
		if a.Capabilities != nil {
			fmt.Fprintf(w, "  capabilities supported=%s allowed=%s\n", strings.Join(a.Capabilities.Supported, ","), strings.Join(a.Capabilities.Allowed, ","))
		} else {
			fmt.Fprintln(w, "  capabilities unknown (older agent)")
		}
	}
	fmt.Fprintf(w, "\nVPN clients (%d)\n", len(status.Clients))
	fmt.Fprintf(w, "%-20s %-24s %-18s %-8s %8s %8s %7s %6s %6s %6s %8s\n", "Session", "Host", "Remote", "Internal", "RX", "TX", "Streams", "Queued", "Flight", "CWND", "Retrans")
	for _, c := range status.Clients {
		fmt.Fprintf(w, "%-20d %-24s %-18s %-8t %8d %8d %7d %6d %6d %6d %8d\n", c.SessionID, c.Hostname, c.Remote, c.Internal, c.RXBytes, c.TXBytes, c.Streams, c.Queued, c.InFlight, c.Window, c.Retransmits)
		for _, route := range c.AcceptedRoutes {
			fmt.Fprintf(w, "  accepted %s via %s\n", route.Prefix, route.AgentID)
		}
	}
	fmt.Fprintf(w, "\nRoutes (%d)\n", len(status.Routes))
	for _, r := range status.Routes {
		fmt.Fprintf(w, "%-20s via %-18s active=%v\n", r.Prefix, r.AgentID, r.Active)
	}
	if status.Selected != "" {
		fmt.Fprintf(w, "Selected: %s\n", status.Selected)
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
		return printJSON(data)
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
			Routes []routing.Route `json:"routes"`
		}
		if err = json.Unmarshal(data, &status); err != nil {
			return err
		}
		if o.json {
			b, _ := json.Marshal(status.Routes)
			return printJSON(b)
		}
		for _, r := range status.Routes {
			fmt.Printf("%-20s via %-18s active=%v\n", r.Prefix, r.AgentID, r.Active)
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
