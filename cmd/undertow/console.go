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
type clientTransferAction func(context.Context, clientFileRequest, func(pivot.TransferProgress)) (pivot.FileMessage, error)
type consoleFeatures struct {
	open           interactiveOpener
	script         scriptOpener
	wasm           wasmOpener
	native         nativeOpener
	bof            bofOpener
	transfer       clientTransferAction
	serverLogPath  string
	serverAttached bool
	stopServer     func() error
}

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
	return consoleCommandWithOptions(options, consoleFeatures{})
}

func consoleCommandWithOptions(options operatorOptions, lifecycle consoleFeatures) error {
	ctx, stop := commandContext()
	defer stop()
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		return callControl(options, method, path, body)
	}
	opener := func(ctx context.Context, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
		return openControlInteractive(ctx, options, agentID, request)
	}
	script := func(ctx context.Context, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
		return openControlScript(ctx, options, agentID, language, source)
	}
	wasm := func(ctx context.Context, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
		return openControlWASM(ctx, options, agentID, module, args, stdin)
	}
	native := func(ctx context.Context, agentID string, module []byte, args []string, data []byte) (*pivot.InteractiveSession, error) {
		return openControlNative(ctx, options, agentID, module, args, data)
	}
	bofOpen := func(ctx context.Context, agentID string, object, arguments []byte) (*pivot.InteractiveSession, error) {
		return openControlBOF(ctx, options, agentID, object, arguments)
	}
	lifecycle.open, lifecycle.script, lifecycle.wasm, lifecycle.native, lifecycle.bof = opener, script, wasm, native, bofOpen
	return runConsole(ctx, os.Stdin, os.Stdout, caller, nil, nil, nil, nil, lifecycle)
}

func runConsole(ctx context.Context, input io.Reader, output io.Writer, call consoleCaller, clientID func() uint64, quit func(), clientRoutes clientRouteAction, events <-chan string, features ...consoleFeatures) error {
	terminalOutput := false
	if file, ok := output.(*os.File); ok && isConsoleTerminal(file) {
		if restore, enabled := enableConsoleOutput(file); enabled {
			defer restore()
			terminalOutput = true
		}
	}
	fmt.Fprintln(output, "Interactive console. Type agents to list agents, help for commands.")
	vpnClient := clientID != nil
	var serverConsole consoleFeatures
	if len(features) > 0 {
		serverConsole = features[0]
	}
	selectedID, selectedLabel := "", ""
	loadedBOFs := newLoadedBOFRegistry()
	loadedArtifacts := newLoadedArtifactRegistry()
	for _, err := range preloadModuleBank(loadedArtifacts, loadedBOFs) {
		fmt.Fprintln(output, "module preload:", err)
	}
	var jobSelection consoleJobSelection
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
		editor.serverAttached = serverConsole.serverAttached
		editor.loadedBOFs = loadedBOFs
		editor.loadedArtifacts = loadedArtifacts
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
				} else if serverConsole.serverAttached {
					fmt.Fprintln(output, "\nConsole detached. Server continues; reconnect with 'undertow server attach'.")
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
						jobSelection = consoleJobSelection{}
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
		if args[0] == "clear" || args[0] == "cls" {
			if len(args) != 1 {
				fmt.Fprintln(output, "error: use clear or cls")
			} else if terminalOutput {
				fmt.Fprint(output, "\x1b[2J\x1b[H")
			}
			continue
		}
		if args[0] == "quit" || args[0] == "exit" {
			if quit != nil {
				quit()
			} else if serverConsole.serverAttached {
				fmt.Fprintln(output, "Console detached. Server continues; reconnect with 'undertow server attach'.")
			}
			return nil
		}
		if args[0] == "background" && (vpnClient || serverConsole.serverAttached) {
			if vpnClient {
				fmt.Fprintln(output, "Console detached. VPN continues in the background; reconnect with 'undertow client attach'.")
			} else {
				fmt.Fprintln(output, "Console detached. Server continues in the background; reconnect with 'undertow server attach'.")
			}
			return nil
		}
		if args[0] == "stop" && len(args) == 1 && serverConsole.stopServer != nil {
			if err := serverConsole.stopServer(); err != nil {
				fmt.Fprintln(output, "error: stop server:", err)
				continue
			}
			fmt.Fprintln(output, "Server stopped.")
			return nil
		}
		if args[0] == "logs" && serverConsole.serverLogPath != "" {
			if len(args) > 2 || (len(args) == 2 && args[1] != "follow") {
				fmt.Fprintln(output, "error: use logs or logs follow")
				continue
			}
			if err := showServerLogs(ctx, output, serverConsole.serverLogPath, len(args) == 2, lines); err != nil {
				fmt.Fprintln(output, "error: logs:", err)
			}
			continue
		}
		if args[0] == "help" {
			if len(args) > 2 {
				if len(args) == 3 && args[1] == "show" {
					fmt.Fprintf(output, "To inspect job %s, use job show %s; use job output %s to read its output. Type help jobs for job commands.\n", args[2], args[2], args[2])
				} else {
					fmt.Fprintln(output, "error: use help [TOPIC]")
				}
				continue
			}
			topic := ""
			if len(args) == 2 {
				topic = args[1]
			}
			if err := printConsoleHelp(output, vpnClient, selectedID != "", serverConsole.serverAttached, topic, consoleHelpOptions{agent: selectedLabel, color: terminalOutput && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb", loadedBOFs: loadedBOFs, loadedArtifacts: loadedArtifacts}); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "load" || args[0] == "unload" || args[0] == "bofs" || args[0] == "modules" {
			var err error
			if args[0] == "bofs" || len(args) > 1 && args[1] == "bof" {
				err = runLoadedBOFManagement(output, loadedBOFs, args, loadedArtifacts)
			} else {
				err = runArtifactManagement(output, loadedArtifacts, loadedBOFs, args)
			}
			if err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "back" {
			if selectedID == "" {
				fmt.Fprintln(output, "Already at the main menu.")
			} else {
				selectedID, selectedLabel = "", ""
				jobSelection = consoleJobSelection{}
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
			jobSelection = consoleJobSelection{}
			fmt.Fprintf(output, "Selected %s (%s). Type help for agent commands.\n", selectedLabel, agent.ID)
			continue
		}
		if args[0] == "shell" || args[0] == "interactive" {
			if selectedID == "" {
				fmt.Fprintln(output, "error: select an agent first with use AGENT_NUMBER")
				continue
			}
			if len(features) == 0 || features[0].open == nil {
				fmt.Fprintln(output, "error: interactive agent sessions unavailable")
				continue
			}
			if err := runInteractiveConsole(ctx, output, editor, features[0].open, selectedID, args[1:]); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if entry := loadedBOFs.get(args[0]); entry != nil {
			if err := runLoadedBOF(ctx, output, editor, call, serverConsole.bof, entry, selectedID, selectedLabel, args[1:]); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if entry := loadedArtifacts.get(args[0]); entry != nil {
			if err := runLoadedArtifact(ctx, output, editor, call, serverConsole.native, serverConsole.wasm, entry, selectedID, selectedLabel, args[1:]); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if selectedID != "" {
			switch args[0] {
			case "agent":
				if len(args) == 2 && (args[1] == "shutdown" || args[1] == "events") {
					args = append(args, selectedID)
				}
			case "session":
				if len(args) == 2 && args[1] == "kill" {
					args = append(args, selectedID)
				}
			case "show":
				args = append([]string{"show", selectedID}, args[1:]...)
			case "job":
				if len(args) >= 2 && args[1] == "start" {
					args = append([]string{"job", "start", selectedID}, args[2:]...)
				}
			case "exec":
				args = append([]string{"exec", selectedID}, args[1:]...)
			case "run-script":
				args = append([]string{"run-script", selectedID}, args[1:]...)
			case "run-wasm":
				args = append([]string{"run-wasm", selectedID}, args[1:]...)
			case "run-native":
				args = append([]string{"run-native", selectedID}, args[1:]...)
			case "run-bof":
				args = append([]string{"run-bof", selectedID}, args[1:]...)
			case "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table":
				args = append([]string{args[0], selectedID}, args[1:]...)
			case "upload", "download":
				args = append([]string{args[0], selectedID}, args[1:]...)
			case "forward":
				if vpnClient && len(args) >= 2 && (args[1] == "add" || args[1] == "del" || args[1] == "list") {
					args = append([]string{"forward", args[1], selectedID}, args[2:]...)
				}
			case "route":
				if len(args) == 3 && (args[1] == "add" || args[1] == "accept") {
					args = append(args, selectedID)
				} else if vpnClient && len(args) == 3 && args[1] == "del" {
					args = append(args, selectedID)
				}
			case "routes":
				args = append(args, selectedID)
			case "relay":
				args = append([]string{"relay", selectedID}, args[1:]...)
			}
		}
		ownClientID := uint64(0)
		if clientID != nil {
			ownClientID = clientID()
		}
		if (args[0] == "upload" || args[0] == "download") && len(features) != 0 && features[0].transfer != nil {
			if err := runConsoleTransfer(ctx, output, editor, features[0].transfer, args); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "run-script" {
			var open scriptOpener
			if len(features) != 0 {
				open = features[0].script
			}
			if err := runConsoleScript(ctx, output, editor, call, open, args); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "run-wasm" {
			var open wasmOpener
			if len(features) != 0 {
				open = features[0].wasm
			}
			if err := runConsoleWASM(ctx, output, editor, call, open, args); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "run-native" {
			var open nativeOpener
			if len(features) != 0 {
				open = features[0].native
			}
			if err := runConsoleNative(ctx, output, editor, call, open, args); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "run-bof" {
			var open bofOpener
			if len(features) != 0 {
				open = features[0].bof
			}
			if err := runConsoleBOF(ctx, output, editor, call, open, args); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
		}
		if args[0] == "jobs" || args[0] == "job" {
			var confirm func(string) bool
			if editor != nil {
				confirm = func(question string) bool {
					editor.showPrompt(question, false)
					select {
					case answer := <-lines:
						answer = strings.ToLower(strings.TrimSpace(answer))
						return answer == "" || answer == "y" || answer == "yes"
					case <-ctx.Done():
						return false
					}
				}
			}
			if err := runConsoleJobCommand(ctx, output, call, args, selectedID, &jobSelection, confirm); err != nil {
				fmt.Fprintln(output, "error:", err)
			}
			continue
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
	fmt.Fprintf(output, "Connected agents (%d):\n", len(agents))
	if len(agents) > 0 {
		fmt.Fprintln(output, "  No.  Hostname              Agent ID                          Virtual IP      Carrier    Path")
	}
	for i, agent := range agents {
		path := "direct"
		if agent.Via != "" {
			path = "via " + agentRouteLabel(agent.Via, agents)
		}
		profile := ""
		if agent.Profile != "" {
			profile = " profile=" + agent.Profile
		}
		fmt.Fprintf(output, "  %-4d %-20s  %-32s  %-15s %s  %s  routes=%d jobs=%d%s\n", i+1, consoleAgentName(agent), agent.ID, agent.VirtualIP, agent.Transport, path, len(agent.AdvertisedRoutes), agent.ActiveJobs, profile)
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
	if args[0] == "session" {
		if len(args) != 3 || args[1] != "kill" {
			return errors.New("use session kill AGENT_NUMBER|ID|HOSTNAME, or select an agent first")
		}
		agents, err := consoleAgents(ctx, call)
		if err != nil {
			return err
		}
		a, err := findConsoleAgent(agents, args[2])
		if err != nil {
			return err
		}
		if _, err := call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(a.ID)+"/kill", nil); err != nil {
			return err
		}
		fmt.Fprintf(output, "Session for %s closed; the agent may reconnect.\n", consoleAgentName(a))
		return nil
	}
	if args[0] == "payload" {
		return runConsolePayload(ctx, output, call, args)
	}
	if args[0] == "agent" && (len(args) == 1 || args[1] != "show") {
		return runConsoleAgentDistribution(ctx, output, call, args)
	}
	if args[0] == "relay" || args[0] == "topology" {
		if vpnClient {
			return errors.New("relay management and topology require a server console")
		}
		return runConsoleRelayCommand(ctx, output, call, args)
	}
	if args[0] == "transports" || args[0] == "start" || args[0] == "stop" {
		if vpnClient {
			return errors.New("transport management requires a server console")
		}
		return runConsoleTransportCommand(ctx, output, call, args)
	}
	if args[0] == "agent" && len(args) == 3 && args[1] == "show" {
		args = []string{"show", args[2]}
	}
	if args[0] == "jobs" || args[0] == "job" {
		return runConsoleJobCommand(ctx, output, call, args, "", nil)
	}
	switch args[0] {
	case "show":
		if len(args) != 2 {
			return errors.New("use agent show AGENT_ID or select an agent and type show")
		}
		data, err := call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		var status struct {
			Agents []control.AgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		for _, agent := range status.Agents {
			if agent.ID == args[1] {
				return renderAgentShow(output, agent)
			}
		}
		return errors.New("agent is not connected")
	case "help":
		if vpnClient {
			fmt.Fprint(output, `VPN client commands:
  status [--json]                Show agents, VPN clients, and routes
  routes                         List configured routes
  route accept CIDR AGENT_ID     Accept an advertised route locally
  route add CIDR AGENT_ID        Add a manual local route via an agent
  route del CIDR                 Remove a locally accepted route
  forward add AGENT_ID BIND TARGET Expose client TCP service on agent
  forward list [AGENT_ID]        List TCP forwards
  forward del AGENT_ID BIND      Stop one TCP forward
  exec AGENT_ID PROGRAM [ARGS]   Run one program on an agent
  run-script AGENT_ID [--background] bash|powershell LOCAL_FILE
  run-wasm AGENT_ID [--background] [--stdin FILE] MODULE [ARGS]
  run-native AGENT_ID [--background] [--data FILE] MODULE [ARGS]
  run-bof AGENT_ID [--background] [--format FORMAT] OBJECT.o [ARGS]
  job start AGENT_ID PROGRAM ... Start a background task
  jobs; job show|output|cancel ID Inspect or stop tasks
  HOST_OP AGENT_ID [ARGS]        Host operations; type use NUMBER then help
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
  transports                    Show active transport listeners
  topology                      Show agent parent/child paths
  start transport NAME [self-signed|tls-cert FILE tls-key FILE] [listen ADDR]
  stop transport NAME [force]    Stop one listener
  routes                         List configured routes
  route add CIDR AGENT_ID        Add an internal route
  route del CIDR                 Remove an internal route
  select AGENT_ID                Select the default agent
  exec AGENT_ID PROGRAM [ARGS]   Run one program on an agent
  run-script AGENT_ID [--background] bash|powershell LOCAL_FILE
  run-wasm AGENT_ID [--background] [--stdin FILE] MODULE [ARGS]
  run-native AGENT_ID [--background] [--data FILE] MODULE [ARGS]
  run-bof AGENT_ID [--background] [--format FORMAT] OBJECT.o [ARGS]
  job start AGENT_ID PROGRAM ... Start a background task
  jobs; job show|output|cancel ID Inspect or stop tasks
  HOST_OP AGENT_ID [ARGS]        Host operations; type use NUMBER then help
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
			Routes []routing.Route     `json:"routes"`
			Agents []control.AgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		fmt.Fprintf(output, "Routes (%d)\n", len(status.Routes))
		for _, route := range status.Routes {
			if len(args) == 2 && route.AgentID != args[1] {
				continue
			}
			fmt.Fprintf(output, "%s via %s active=%t\n", route.Prefix, agentRouteLabel(route.AgentID, status.Agents), route.Active)
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
	case "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table":
		if len(args) < 2 {
			return errors.New("select an agent with use NUMBER, or provide AGENT_ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(args[1])+"/exec", pivot.ExecRequest{Builtin: args[0], Args: args[2:]})
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
		if !vpnClient || clientRoutes == nil {
			return errors.New("internal mode can be changed in the VPN client console")
		}
		return clientRoutes(ctx, args, output)
	case "vpn":
		if !vpnClient || clientRoutes == nil {
			return errors.New("VPN mode can be changed in the VPN client console")
		}
		return clientRoutes(ctx, args, output)
	case "forward":
		if !vpnClient || ownClientID == 0 {
			return errors.New("agent TCP forwards are configured in the VPN client console")
		}
		base := fmt.Sprintf("/v1/clients/%d/forwards", ownClientID)
		if len(args) == 2 && args[1] == "list" || len(args) == 3 && args[1] == "list" {
			data, err := call(ctx, http.MethodGet, base, nil)
			if err != nil {
				return err
			}
			var forwards []control.ForwardInfo
			if err := json.Unmarshal(data, &forwards); err != nil {
				return err
			}
			for _, forward := range forwards {
				if len(args) == 3 && forward.AgentID != args[2] {
					continue
				}
				fmt.Fprintf(output, "%s on %s -> client %s\n", forward.Bind, forward.AgentID, forward.Target)
			}
			return nil
		}
		if len(args) == 5 && args[1] == "add" {
			data, err := call(ctx, http.MethodPost, base, map[string]string{"agent_id": args[2], "bind": args[3], "target": args[4]})
			if err != nil {
				return err
			}
			var forward control.ForwardInfo
			if err := json.Unmarshal(data, &forward); err != nil {
				return err
			}
			fmt.Fprintf(output, "Agent %s listening on %s -> client %s\n", forward.AgentID, forward.Bind, forward.Target)
			return nil
		}
		if len(args) == 4 && args[1] == "del" {
			path := base + "?agent_id=" + url.QueryEscape(args[2]) + "&bind=" + url.QueryEscape(args[3])
			if _, err := call(ctx, http.MethodDelete, path, nil); err != nil {
				return err
			}
			fmt.Fprintln(output, "Agent TCP forward stopped.")
			return nil
		}
		return errors.New("use forward add AGENT_ID BIND TARGET, forward list [AGENT_ID], or forward del AGENT_ID BIND")
	default:
		return fmt.Errorf("unknown command %q; type help", args[0])
	}
}
