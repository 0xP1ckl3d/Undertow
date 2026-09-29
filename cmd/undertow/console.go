package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode"

	"undertow/internal/pivot"
	"undertow/internal/routing"
)

type consoleCaller func(context.Context, string, string, any) ([]byte, error)
type clientRouteAction func(context.Context, []string, io.Writer) error

func consoleCommand(args []string) error {
	options, err := parseOperator(args)
	if err != nil {
		return err
	}
	if len(options.positional) != 0 {
		return errors.New("usage: undertow console [--control IP:PORT] [--control-token-file PATH]")
	}
	ctx, stop := commandContext()
	defer stop()
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		return callControl(options, method, path, body)
	}
	return runConsole(ctx, os.Stdin, os.Stdout, caller, nil, nil, nil)
}

func runConsole(ctx context.Context, input io.Reader, output io.Writer, call consoleCaller, clientID func() uint64, quit func(), clientRoutes clientRouteAction) error {
	fmt.Fprintln(output, "Interactive console. Type help for commands; quit to exit.")
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 8192)
	lines := make(chan string)
	scanDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		scanDone <- scanner.Err()
	}()
	for {
		fmt.Fprint(output, "undertow> ")
		var line string
		select {
		case <-ctx.Done():
			return nil
		case err := <-scanDone:
			if quit != nil {
				quit()
			}
			return err
		case line = <-lines:
		}
		args, err := splitConsoleCommand(line)
		if err != nil {
			fmt.Fprintln(output, "error:", err)
			continue
		}
		if len(args) == 0 {
			continue
		}
		if args[0] == "quit" || args[0] == "exit" {
			if quit != nil {
				quit()
			}
			return nil
		}
		ownClientID := uint64(0)
		if clientID != nil {
			ownClientID = clientID()
		}
		if err := runConsoleCommand(ctx, output, call, clientID != nil, ownClientID, clientRoutes, args); err != nil {
			fmt.Fprintln(output, "error:", err)
		}
	}
}

func splitConsoleCommand(line string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	started := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

func runConsoleCommand(ctx context.Context, output io.Writer, call consoleCaller, vpnClient bool, ownClientID uint64, clientRoutes clientRouteAction, args []string) error {
	switch args[0] {
	case "help":
		if vpnClient {
			fmt.Fprint(output, `VPN client commands:
  status [--json]                Show agents, VPN clients, and routes
  routes                         List configured routes
  route accept CIDR AGENT_ID     Accept an advertised route locally
  route add CIDR AGENT_ID        Add a manual local route via an agent
  route del CIDR                 Remove a locally accepted route
  exec AGENT_ID PROGRAM [ARGS]   Run one program on an agent
  internal on|off                Change this VPN client's pivot mode
  quit                           Stop the VPN and leave the console
Quote arguments containing spaces.
`)
			return nil
		}
		fmt.Fprint(output, `Server operator commands:
  status [--json]                Show agents, VPN clients, and routes
  routes                         List configured routes
  route add CIDR AGENT_ID        Add an internal route
  route del CIDR                 Remove an internal route
  select AGENT_ID                Select the default agent
  exec AGENT_ID PROGRAM [ARGS]   Run one program on an agent
  quit                           Leave the console
Quote arguments containing spaces. Commands run only when submitted.
`)
		return nil
	case "status", "agents", "clients":
		if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
			return errors.New("use status [--json]")
		}
		data, err := call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		if len(args) == 2 {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, data, "", "  "); err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, pretty.String())
			return err
		}
		return renderStatus(output, data)
	case "routes":
		if vpnClient && clientRoutes != nil {
			return clientRoutes(ctx, args, output)
		}
		data, err := call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		var status struct {
			Routes []routing.Route `json:"routes"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		fmt.Fprintf(output, "Routes (%d)\n", len(status.Routes))
		for _, route := range status.Routes {
			fmt.Fprintf(output, "%s via %s active=%t\n", route.Prefix, route.AgentID, route.Active)
		}
		return nil
	case "route":
		if vpnClient {
			if clientRoutes == nil {
				return errors.New("client route control is unavailable")
			}
			return clientRoutes(ctx, args, output)
		}
		if len(args) == 4 && args[1] == "add" {
			_, err := call(ctx, http.MethodPost, "/v1/routes", map[string]string{"prefix": args[2], "agent_id": args[3]})
			if err == nil {
				fmt.Fprintln(output, "Route added.")
			}
			return err
		}
		if len(args) == 3 && args[1] == "del" {
			_, err := call(ctx, http.MethodDelete, "/v1/routes?prefix="+url.QueryEscape(args[2]), nil)
			if err == nil {
				fmt.Fprintln(output, "Route removed.")
			}
			return err
		}
		return errors.New("use route add CIDR AGENT_ID or route del CIDR")
	case "select":
		if vpnClient {
			return errors.New("agent selection requires the server operator console")
		}
		if len(args) != 2 {
			return errors.New("use select AGENT_ID")
		}
		_, err := call(ctx, http.MethodPost, "/v1/selection", map[string]string{"agent_id": args[1]})
		if err == nil {
			fmt.Fprintln(output, "Agent selected.")
		}
		return err
	case "exec":
		if len(args) < 3 {
			return errors.New("use exec AGENT_ID PROGRAM [ARGS]")
		}
		data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(args[1])+"/exec", pivot.ExecRequest{Argv: args[2:]})
		if err != nil {
			return err
		}
		var result pivot.ExecResult
		if err := json.Unmarshal(data, &result); err != nil {
			return err
		}
		fmt.Fprint(output, result.Stdout)
		fmt.Fprint(output, result.Stderr)
		if result.Error != "" {
			return errors.New(result.Error)
		}
		fmt.Fprintf(output, "[exit %d]\n", result.ExitCode)
		return nil
	case "internal":
		if ownClientID == 0 {
			return errors.New("internal mode can be changed in the VPN client console")
		}
		if len(args) != 2 || args[1] != "on" && args[1] != "off" {
			return errors.New("use internal on or internal off")
		}
		_, err := call(ctx, http.MethodPost, fmt.Sprintf("/v1/clients/%d/internal", ownClientID), map[string]bool{"enabled": args[1] == "on"})
		if err == nil {
			fmt.Fprintf(output, "Internal pivots %s for new flows.\n", args[1])
		}
		return err
	default:
		return fmt.Errorf("unknown command %q; type help", args[0])
	}
}
