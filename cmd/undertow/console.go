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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"undertow/internal/control"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

type consoleCaller func(context.Context, string, string, any) ([]byte, error)
type clientRouteAction func(context.Context, []string, io.Writer) error

type clientFileRequest struct {
	AgentID    string `json:"agent_id"`
	Operation  string `json:"operation"`
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
}

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
	return runConsole(ctx, os.Stdin, os.Stdout, caller, nil, nil, nil, nil)
}

func runConsole(ctx context.Context, input io.Reader, output io.Writer, call consoleCaller, clientID func() uint64, quit func(), clientRoutes clientRouteAction, events <-chan string) error {
	fmt.Fprintln(output, "Interactive console. Type agents to list agents, help for commands.")
	vpnClient := clientID != nil
	selectedID, selectedLabel := "", ""
	known := make(map[string]control.AgentInfo)
	type agentRefresh struct {
		agents []control.AgentInfo
		err    error
	}
	refreshes := make(chan agentRefresh, 1)
	refreshPending := false
	startRefresh := func() {
		if refreshPending {
			return
		}
		refreshPending = true
		go func() {
			agents, err := consoleAgents(ctx, call)
			select {
			case refreshes <- agentRefresh{agents: agents, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	lines := make(chan string)
	scanDone := make(chan error, 1)
	var editor *consoleEditor
	if file, ok := input.(*os.File); ok && file == os.Stdin && isConsoleTerminal(file) {
		restore, err := setConsoleRaw(file)
		if err != nil {
			return err
		}
		defer restore()
		editor = newConsoleEditor(output, vpnClient)
		go func() { scanDone <- editor.read(ctx, input, lines) }()
	} else {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 8192)
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
	}
	if editor != nil {
		startRefresh()
	}
	promptShown := false
	for {
		if !promptShown {
			prompt := "undertow> "
			if selectedID != "" {
				prompt = fmt.Sprintf("undertow[%s]> ", selectedLabel)
			}
			if editor != nil {
				editor.showPrompt(prompt, selectedID != "")
			} else {
				fmt.Fprint(output, prompt)
			}
			promptShown = true
		}
		var line string
		select {
		case <-ctx.Done():
			return nil
		case err := <-scanDone:
			if editor != nil && errors.Is(err, io.EOF) {
				if vpnClient {
					fmt.Fprintln(output, "\nConsole detached. VPN continues; reconnect with 'undertow client attach'.")
				}
				return nil
			}
			if quit != nil {
				quit()
			}
			return err
		case line = <-lines:
			promptShown = false
		case event := <-events:
			if editor != nil {
				editor.notice(event)
			} else {
				fmt.Fprintf(output, "\n[%s]\n", event)
				promptShown = false
			}
			continue
		case <-ticker.C:
			if editor != nil {
				startRefresh()
			}
			continue
		case refresh := <-refreshes:
			refreshPending = false
			if refresh.err != nil {
				continue
			}
			next := make(map[string]control.AgentInfo, len(refresh.agents))
			emitted := false
			for _, agent := range refresh.agents {
				next[agent.ID] = agent
				if _, ok := known[agent.ID]; !ok {
					if editor != nil {
						editor.notice(fmt.Sprintf("Agent connected: %s (%s)", consoleAgentName(agent), shortAgentID(agent.ID)))
					} else {
						fmt.Fprintf(output, "\n[Agent connected: %s (%s)]\n", consoleAgentName(agent), shortAgentID(agent.ID))
					}
					emitted = true
				}
			}
			for id, agent := range known {
				if _, ok := next[id]; !ok {
					if editor != nil {
						editor.notice(fmt.Sprintf("Agent lost: %s (%s)", consoleAgentName(agent), shortAgentID(id)))
					} else {
						fmt.Fprintf(output, "\n[Agent lost: %s (%s)]\n", consoleAgentName(agent), shortAgentID(id))
					}
					emitted = true
					if selectedID == id {
						selectedID, selectedLabel = "", ""
					}
				}
			}
			known = next
			if emitted {
				promptShown = false
			}
			continue
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
		if args[0] == "background" && vpnClient {
			fmt.Fprintln(output, "Console detached. VPN continues in the background; reconnect with 'undertow client attach'.")
			return nil
		}
		if args[0] == "help" {
			printConsoleHelp(output, vpnClient, selectedID != "")
			continue
		}
		if args[0] == "back" {
			if selectedID == "" {
				fmt.Fprintln(output, "Already at the main menu.")
			} else {
				selectedID, selectedLabel = "", ""
			}
			continue
		}
		if args[0] == "agents" || args[0] == "use" || args[0] == "select" {
			agents, err := consoleAgents(ctx, call)
			if err != nil {
				fmt.Fprintln(output, "error:", err)
				continue
			}
			if args[0] == "agents" || len(args) == 1 {
				printConsoleAgents(output, agents)
				continue
			}
			if len(args) != 2 {
				fmt.Fprintln(output, "error: use AGENT_NUMBER or AGENT_ID")
				continue
			}
			agent, err := findConsoleAgent(agents, args[1])
			if err != nil {
				fmt.Fprintln(output, "error:", err)
				continue
			}
			selectedID, selectedLabel = agent.ID, consoleAgentName(agent)
			fmt.Fprintf(output, "Selected %s (%s). Type help for agent commands.\n", selectedLabel, agent.ID)
			continue
		}
		if selectedID != "" {
			switch args[0] {
			case "exec":
				args = append([]string{"exec", selectedID}, args[1:]...)
			case "upload", "download":
				args = append([]string{args[0], selectedID}, args[1:]...)
			case "route":
				if len(args) == 3 && (args[1] == "add" || args[1] == "accept") {
					args = append(args, selectedID)
				} else if vpnClient && len(args) == 3 && args[1] == "del" {
					args = append(args, selectedID)
				}
			case "routes":
				args = append(args, selectedID)
			}
		}
		ownClientID := uint64(0)
		if clientID != nil {
			ownClientID = clientID()
		}
		if err := runConsoleCommand(ctx, output, call, vpnClient, ownClientID, clientRoutes, args); err != nil {
			fmt.Fprintln(output, "error:", err)
		}
	}
}

func consoleAgents(ctx context.Context, call consoleCaller) ([]control.AgentInfo, error) {
	data, err := call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return nil, err
	}
	var status struct {
		Agents []control.AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	sort.Slice(status.Agents, func(i, j int) bool {
		if status.Agents[i].Hostname != status.Agents[j].Hostname {
			return status.Agents[i].Hostname < status.Agents[j].Hostname
		}
		return status.Agents[i].ID < status.Agents[j].ID
	})
	return status.Agents, nil
}

func shortAgentID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func consoleAgentName(agent control.AgentInfo) string {
	if agent.Hostname != "" {
		return agent.Hostname
	}
	return shortAgentID(agent.ID)
}

func printConsoleAgents(output io.Writer, agents []control.AgentInfo) {
	fmt.Fprintf(output, "Agents (%d):\n", len(agents))
	for i, agent := range agents {
		fmt.Fprintf(output, "  %d  %-20s  %s  %s  routes=%d\n", i+1, consoleAgentName(agent), shortAgentID(agent.ID), agent.VirtualIP, len(agent.AdvertisedRoutes))
	}
	if len(agents) > 0 {
		fmt.Fprintln(output, "Use an agent with: use NUMBER")
	}
}

func findConsoleAgent(agents []control.AgentInfo, target string) (control.AgentInfo, error) {
	if n, err := strconv.Atoi(target); err == nil {
		if n > 0 && n <= len(agents) {
			return agents[n-1], nil
		}
		return control.AgentInfo{}, errors.New("agent number is out of range")
	}
	var match control.AgentInfo
	for _, agent := range agents {
		if agent.ID == target || strings.HasPrefix(agent.ID, target) || agent.Hostname == target {
			if match.ID != "" {
				return control.AgentInfo{}, errors.New("agent name or ID prefix is ambiguous; use its number")
			}
			match = agent
		}
	}
	if match.ID == "" {
		return control.AgentInfo{}, errors.New("agent not found; type agents")
	}
	return match, nil
}

func printConsoleHelp(output io.Writer, vpnClient, selected bool) {
	if selected {
		fmt.Fprint(output, `Agent commands:
  exec PROGRAM [ARGS]    Run a program on the selected agent
  routes                 Show routes and advertisements
  route add CIDR         Add a route through this agent
  route del CIDR         Remove a route
  status                 Show full status
  back                   Return to the main menu
  help                   Show this menu
  quit                   Exit the console
Quote paths or arguments containing spaces. Programs run without a shell.
`)
		if vpnClient {
			fmt.Fprintln(output, "  route accept CIDR      Accept an advertised route from this agent")
			fmt.Fprintln(output, "  upload LOCAL REMOTE    Copy a local file to this agent")
			fmt.Fprintln(output, "  download REMOTE LOCAL  Copy a file from this agent")
			fmt.Fprintln(output, "  background             Detach console; keep VPN running")
		}
		return
	}
	if vpnClient {
		fmt.Fprint(output, `VPN client menu:
  agents                 List connected agents by number
  use NUMBER             Enter an agent (ID prefix or hostname also works)
  status                 Show agents, VPN clients, and routes
  routes                 Show advertised and locally accepted routes
  internal on|off        Change this client's global pivot mode
  help                   Show this menu
  background             Detach console; keep VPN running
  quit                   Stop the VPN and exit
Inside an agent, use exec PROGRAM, upload LOCAL REMOTE, download REMOTE LOCAL, or route accept CIDR.
`)
		return
	}
	fmt.Fprint(output, `Server operator menu:
  agents                 List connected agents by number
  use NUMBER             Enter an agent (ID prefix or hostname also works)
  status                 Show agents, VPN clients, and routes
  routes                 Show global routes
  route del CIDR         Remove a global route
  help                   Show this menu
  quit                   Exit the console
Inside an agent, use exec PROGRAM or route add CIDR.
`)
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
  upload AGENT_ID LOCAL REMOTE   Copy a file to an agent
  download AGENT_ID REMOTE LOCAL Copy a file from an agent
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
		if len(args) > 2 {
			return errors.New("use routes")
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
			if len(args) == 2 && route.AgentID != args[1] {
				continue
			}
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
	case "upload", "download":
		if !vpnClient {
			return errors.New("file transfer is available in the VPN client console")
		}
		if len(args) != 4 {
			return errors.New("use upload AGENT_ID LOCAL REMOTE or download AGENT_ID REMOTE LOCAL")
		}
		request := clientFileRequest{AgentID: args[1], Operation: args[0]}
		if args[0] == "upload" {
			request.LocalPath, request.RemotePath = args[2], args[3]
		} else {
			request.LocalPath, request.RemotePath = args[3], args[2]
		}
		local, err := filepath.Abs(request.LocalPath)
		if err != nil {
			return err
		}
		request.LocalPath = local
		fmt.Fprintf(output, "%s in progress...\n", strings.Title(request.Operation))
		data, err := call(ctx, http.MethodPost, "/v1/file/transfer", request)
		if err != nil {
			return err
		}
		var result pivot.FileMessage
		if err := json.Unmarshal(data, &result); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s complete: %d bytes, SHA-256 %s\n", strings.Title(request.Operation), result.Size, result.SHA256)
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
