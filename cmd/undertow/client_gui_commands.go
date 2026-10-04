//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

type guiCommandResult struct {
	Output    string               `json:"output"`
	OpenShell bool                 `json:"open_shell,omitempty"`
	ModuleRun *guiModuleCommandRun `json:"module_run,omitempty"`
}

func (g *guiServer) listGUIConsoleModules(verb string) guiCommandResult {
	if g.modules == nil {
		return guiCommandResult{Output: "No client module bank is available.\n"}
	}
	var out strings.Builder
	for _, module := range g.modules.list() {
		if verb == "bofs" && module.Kind != "bof" {
			continue
		}
		fmt.Fprintf(&out, "%-25s %-7s %s\n", module.Name, module.Kind, module.Description)
	}
	if out.Len() == 0 {
		out.WriteString("No loaded commands.\n")
	}
	return guiCommandResult{Output: out.String()}
}

func (g *guiServer) loadGUIConsoleModule(args []string) (guiCommandResult, error) {
	if g.modules == nil {
		return guiCommandResult{}, errors.New("client module bank is unavailable")
	}
	if len(args) < 3 {
		return guiCommandResult{}, errors.New("use load module|wasm|bof FILE [NAME] [--format FORMAT]")
	}
	g.modules.mu.Lock()
	defer g.modules.mu.Unlock()
	var out bytes.Buffer
	var err error
	if args[1] == "bof" {
		err = runLoadedBOFManagement(&out, g.modules.bofs, args, g.modules.artifacts)
	} else {
		err = runArtifactManagement(&out, g.modules.artifacts, g.modules.bofs, args)
	}
	if err != nil {
		return guiCommandResult{}, err
	}
	return guiCommandResult{Output: out.String()}, nil
}

func (g *guiServer) unloadGUIConsoleModule(args []string) (guiCommandResult, error) {
	if g.modules == nil {
		return guiCommandResult{}, errors.New("client module bank is unavailable")
	}
	if len(args) != 3 || args[1] != "bof" && args[1] != "module" && args[1] != "wasm" {
		return guiCommandResult{}, errors.New("use unload module|wasm|bof NAME")
	}
	for _, module := range g.modules.list() {
		if module.Name == args[2] {
			if module.Kind != args[1] {
				return guiCommandResult{}, fmt.Errorf("%s is a %s, not a %s", module.Name, module.Kind, args[1])
			}
			if err := g.modules.unload(module.Name); err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: "Unloaded " + module.Name + " from this client.\n"}, nil
		}
	}
	return guiCommandResult{}, fmt.Errorf("%s is not loaded", args[2])
}

func (g *guiServer) runGUIConsoleModule(ctx context.Context, agentID string, module guiModuleInfo, args []string) (guiCommandResult, error) {
	background, inputPath, values := false, "", make([]string, 0, len(args))
	options := true
	for i := 0; i < len(args); i++ {
		switch {
		case options && args[i] == "--":
			options = false
		case options && args[i] == "--background":
			background = true
		case options && ((module.Kind == "wasm" && args[i] == "--stdin") || (module.Kind == "module" && args[i] == "--data")):
			if inputPath != "" || i+1 >= len(args) {
				return guiCommandResult{}, fmt.Errorf("%s requires one local file", args[i])
			}
			i++
			inputPath = args[i]
		default:
			values = append(values, args[i])
		}
	}
	var input []byte
	if inputPath != "" {
		var err error
		input, err = readMemoryFileOption(inputPath, 64<<10, true)
		if err != nil {
			return guiCommandResult{}, err
		}
	}
	if background {
		job, _, err := g.startModule(ctx, agentID, module.Name, values, input, true)
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: fmt.Sprintf("Started %s job %s on this agent. Output is retained in Jobs.\n", module.Name, job.ID)}, nil
	}
	return guiCommandResult{ModuleRun: &guiModuleCommandRun{Name: module.Name, Args: values, Input: input}}, nil
}

type guiModuleCommandRun struct {
	Name  string   `json:"name"`
	Args  []string `json:"args"`
	Input []byte   `json:"input,omitempty"`
}

// runAgentGUICommand parses the familiar attached-agent command vocabulary in
// Go and invokes the same structured control APIs used by the terminal console.
// It never runs the terminal console or interprets rendered console output.
func (g *guiServer) runAgentGUICommand(ctx context.Context, agentID, line string) (guiCommandResult, error) {
	args, err := splitConsoleCommand(line)
	if err != nil {
		return guiCommandResult{}, err
	}
	if len(args) == 0 {
		return guiCommandResult{}, nil
	}
	claims, err := g.actionClaims()
	if err != nil {
		return guiCommandResult{}, err
	}
	ctx = control.WithActionClaims(ctx, claims)
	call := func(method, path string, body any) ([]byte, error) { return g.client.call(ctx, method, path, body) }
	base := "/v1/agents/" + url.PathEscape(agentID)
	switch args[0] {
	case "help", "?":
		if len(args) == 2 && g.modules != nil {
			for _, module := range g.modules.list() {
				if module.Name == args[1] {
					return guiCommandResult{Output: fmt.Sprintf("%s (%s)\n%s\n\n%s\nSource: %s\n", module.Name, module.Kind, module.Usage, module.Help, module.Path)}, nil
				}
			}
		}
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use help [LOADED_MODULE]")
		}
		return guiCommandResult{Output: "AGENT COMMANDS\n  show                    Agent details\n  pwd, ls [PATH], stat PATH\n  whoami, ps, privileges, env [NAME]\n  interfaces, dns, route-table\n  screens; screenshot [NUMBER]  List screens or capture to server history\n  exec PROGRAM [ARGS]     Run one program\n  job start PROGRAM ...   Start background job\n  jobs; job show|output|cancel|delete ID\n  modules; bofs; help MODULE\n  load module|wasm|bof FILE [NAME] [--format FORMAT]\n  unload module|wasm|bof NAME\n  MODULE [ARGS] [--background] [--stdin|--data FILE]\n  run-script [--background] bash|powershell FILE\n  run-wasm [--background] [--stdin FILE] MODULE.wasm [ARGS]\n  run-native [--background] [--data FILE] MODULE.module [ARGS]\n  run-bof [--background] [--format FMT] OBJECT.o [ARGS]\n  relay start [BIND]; relay list; relay stop BIND\n  route accept CIDR; route add CIDR; route del CIDR\n  forward add BIND TARGET; forward list; forward del BIND\n  agent events            Recent connection events\n  shell                   Open the separate live shell panel\n\nCommands target this workspace agent. Quotes preserve spaces. Local file paths refer to the Undertow client host. No module or shell starts until requested.\n"}, nil
	case "modules", "bofs":
		if len(args) != 1 {
			return guiCommandResult{}, fmt.Errorf("use %s", args[0])
		}
		return g.listGUIConsoleModules(args[0]), nil
	case "load":
		return g.loadGUIConsoleModule(args)
	case "unload":
		return g.unloadGUIConsoleModule(args)
	case "shell":
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use shell")
		}
		return guiCommandResult{Output: "Live shell panel opened. Select Start live shell to connect.\n", OpenShell: true}, nil
	case "show":
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use show")
		}
		data, err := call(http.MethodGet, "/v1/status", nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var status struct {
			Agents []control.AgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return guiCommandResult{}, err
		}
		var a control.AgentInfo
		for _, candidate := range status.Agents {
			if candidate.ID == agentID {
				a = candidate
				break
			}
		}
		if a.ID == "" {
			return guiCommandResult{}, fmt.Errorf("agent %s is not connected", agentID)
		}
		return guiCommandResult{Output: fmt.Sprintf("Agent: %s\nID: %s\nOS: %s/%s\nCarrier: %s\nRemote: %s\nVia: %s\nLast seen: %s\n", a.Hostname, a.ID, a.OS, a.Arch, a.Transport, a.Remote, a.Via, a.LastSeen.Local().Format("2006-01-02 15:04:05"))}, nil
	case "screens":
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use screens")
		}
		data, err := call(http.MethodGet, base+"/screens", nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var screens []pivot.ScreenInfo
		if err := json.Unmarshal(data, &screens); err != nil {
			return guiCommandResult{}, err
		}
		var out strings.Builder
		fmt.Fprintf(&out, "Screens (%d):\n", len(screens))
		for _, screen := range screens {
			fmt.Fprintf(&out, "  %d  %s  %dx%d  %s\n", screen.Number, screen.Name, screen.Width, screen.Height, screen.Foreground)
		}
		return guiCommandResult{Output: out.String()}, nil
	case "screenshot":
		if len(args) > 2 {
			return guiCommandResult{}, errors.New("use screenshot [NUMBER]")
		}
		var numbers []int
		if len(args) == 2 {
			number, err := strconv.Atoi(args[1])
			if err != nil || number < 1 || number > 64 {
				return guiCommandResult{}, errors.New("use screenshot [NUMBER]")
			}
			numbers = []int{number}
		} else {
			data, err := call(http.MethodGet, base+"/screens", nil)
			if err != nil {
				return guiCommandResult{}, err
			}
			var screens []pivot.ScreenInfo
			if err := json.Unmarshal(data, &screens); err != nil {
				return guiCommandResult{}, err
			}
			for _, screen := range screens {
				numbers = append(numbers, screen.Number)
			}
		}
		if len(numbers) == 0 {
			return guiCommandResult{}, errors.New("no screens available")
		}
		var out strings.Builder
		for _, number := range numbers {
			data, err := call(http.MethodPost, base+"/screenshots", map[string]int{"screen": number})
			if err != nil {
				return guiCommandResult{Output: out.String()}, err
			}
			var item control.ScreenshotInfo
			if err := json.Unmarshal(data, &item); err != nil {
				return guiCommandResult{Output: out.String()}, err
			}
			fmt.Fprintf(&out, "Screen %d captured to server history: %s (%d bytes, SHA-256 %s)\n", number, item.ID, item.Size, item.SHA256)
		}
		return guiCommandResult{Output: out.String()}, nil
	case "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table":
		data, err := call(http.MethodPost, base+"/exec", pivot.ExecRequest{Builtin: args[0], Args: args[1:]})
		if err != nil {
			return guiCommandResult{}, err
		}
		var result pivot.ExecResult
		if err := json.Unmarshal(data, &result); err != nil {
			return guiCommandResult{}, err
		}
		if result.Error != "" {
			return guiCommandResult{Output: result.Stdout + result.Stderr}, errors.New(result.Error)
		}
		return guiCommandResult{Output: result.Stdout + result.Stderr}, nil
	case "exec":
		if len(args) < 2 {
			return guiCommandResult{}, errors.New("use exec PROGRAM [ARGS]")
		}
		data, err := call(http.MethodPost, base+"/exec", pivot.ExecRequest{Argv: args[1:]})
		if err != nil {
			return guiCommandResult{}, err
		}
		var result pivot.ExecResult
		if err := json.Unmarshal(data, &result); err != nil {
			return guiCommandResult{}, err
		}
		output := result.Stdout + result.Stderr
		if result.Error != "" {
			output += result.Error + "\n"
		}
		return guiCommandResult{Output: fmt.Sprintf("%s[exit %d]\n", output, result.ExitCode)}, nil
	case "jobs":
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use jobs")
		}
		data, err := call(http.MethodGet, "/v1/jobs?agent_id="+url.QueryEscape(agentID), nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var jobs []control.JobInfo
		if err := json.Unmarshal(data, &jobs); err != nil {
			return guiCommandResult{}, err
		}
		if len(jobs) == 0 {
			return guiCommandResult{Output: "No jobs for this agent.\n"}, nil
		}
		var out strings.Builder
		for _, job := range jobs {
			fmt.Fprintf(&out, "%s  %-12s %-10s %d bytes\n", job.ID, job.Kind, job.State, job.OutputBytes)
		}
		return guiCommandResult{Output: out.String()}, nil
	case "job":
		if len(args) >= 3 && args[1] == "start" {
			data, err := call(http.MethodPost, base+"/jobs", map[string]any{"argv": args[2:]})
			if err != nil {
				return guiCommandResult{}, err
			}
			var job control.JobInfo
			if err := json.Unmarshal(data, &job); err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: "Started job " + job.ID + "\n"}, nil
		}
		if len(args) != 3 {
			return guiCommandResult{}, errors.New("use job start PROGRAM ... or job show|output|cancel|delete ID")
		}
		jobPath := "/v1/jobs/" + url.PathEscape(args[2])
		data, err := call(http.MethodGet, jobPath, nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var job control.JobInfo
		if err := json.Unmarshal(data, &job); err != nil {
			return guiCommandResult{}, err
		}
		if job.AgentID != agentID {
			return guiCommandResult{}, errors.New("job belongs to another agent")
		}
		switch args[1] {
		case "show":
			return guiCommandResult{Output: fmt.Sprintf("Job: %s\nState: %s\nStarted: %s\nOutput: %d bytes\n", job.ID, job.State, job.Started.Local().Format("2006-01-02 15:04:05"), job.OutputBytes)}, nil
		case "output":
			data, err := call(http.MethodGet, jobPath+"/output", nil)
			if err != nil {
				return guiCommandResult{}, err
			}
			if err := json.Unmarshal(data, &job); err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: job.Output}, nil
		case "cancel", "stop":
			_, err = call(http.MethodPost, jobPath+"/cancel", map[string]any{})
			if err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: "Job cancelled.\n"}, nil
		case "delete":
			_, err = call(http.MethodDelete, jobPath, nil)
			if err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: "Job deleted.\n"}, nil
		default:
			return guiCommandResult{}, errors.New("use job show|output|cancel|delete ID")
		}
	case "agent":
		if len(args) == 2 && args[1] == "events" {
			data, err := call(http.MethodGet, base+"/events", nil)
			if err != nil {
				return guiCommandResult{}, err
			}
			var events []control.LifecycleEvent
			if err := json.Unmarshal(data, &events); err != nil {
				return guiCommandResult{}, err
			}
			if len(events) == 0 {
				return guiCommandResult{Output: "No recent lifecycle events.\n"}, nil
			}
			var out strings.Builder
			for _, event := range events {
				fmt.Fprintf(&out, "%s  %s\n", event.At.Local().Format("15:04:05"), event.Kind)
			}
			return guiCommandResult{Output: out.String()}, nil
		}
		return guiCommandResult{}, errors.New("use agent events")
	case "relay":
		return g.runAgentGUIRelay(ctx, call, agentID, args)
	case "route":
		return g.runAgentGUIRoute(ctx, agentID, args)
	case "forward":
		return g.runAgentGUIForward(call, agentID, args)
	default:
		if g.modules != nil {
			for _, module := range g.modules.list() {
				if module.Name == args[0] {
					return g.runGUIConsoleModule(ctx, agentID, module, args[1:])
				}
			}
		}
		return guiCommandResult{}, fmt.Errorf("unknown agent command %q; type help", args[0])
	}
}

func (g *guiServer) runAgentGUIRelay(_ context.Context, call func(string, string, any) ([]byte, error), agentID string, args []string) (guiCommandResult, error) {
	base := "/v1/agents/" + url.PathEscape(agentID) + "/relays"
	if len(args) == 2 && args[1] == "list" {
		data, err := call(http.MethodGet, "/v1/relays", nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var relays []control.RelayInfo
		if err := json.Unmarshal(data, &relays); err != nil {
			return guiCommandResult{}, err
		}
		if len(relays) == 0 {
			return guiCommandResult{Output: "No relay listeners.\n"}, nil
		}
		var out strings.Builder
		for _, relay := range relays {
			if relay.AgentID == agentID {
				out.WriteString(relay.Bind + "\n")
			}
		}
		return guiCommandResult{Output: out.String()}, nil
	}
	if len(args) >= 2 && args[1] == "start" && len(args) <= 3 {
		bind := ""
		if len(args) == 3 {
			bind = args[2]
		}
		data, err := call(http.MethodPost, base, map[string]string{"bind": bind})
		if err != nil {
			return guiCommandResult{}, err
		}
		var relay control.RelayInfo
		if err := json.Unmarshal(data, &relay); err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Relay listening on " + relay.Bind + "\n"}, nil
	}
	if len(args) == 3 && args[1] == "stop" {
		_, err := call(http.MethodDelete, base+"?bind="+url.QueryEscape(args[2]), nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Relay stopped.\n"}, nil
	}
	return guiCommandResult{}, errors.New("use relay start [BIND], relay list, or relay stop BIND")
}

func (g *guiServer) runAgentGUIRoute(ctx context.Context, agentID string, args []string) (guiCommandResult, error) {
	if len(args) == 3 && (args[1] == "accept" || args[1] == "add") {
		_, err := g.client.AcceptClientRoute(ctx, args[2], agentID, args[1] == "add")
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Route accepted and installed for this client.\n"}, nil
	}
	if len(args) == 3 && (args[1] == "del" || args[1] == "delete") {
		if err := g.client.RemoveClientRoute(ctx, args[2], agentID); err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Client route removed locally and on the server.\n"}, nil
	}
	return guiCommandResult{}, errors.New("use route accept CIDR, route add CIDR, or route del CIDR")
}

func (g *guiServer) runAgentGUIForward(call func(string, string, any) ([]byte, error), agentID string, args []string) (guiCommandResult, error) {
	clientID := g.client.id()
	if clientID == 0 {
		return guiCommandResult{}, errors.New("client is disconnected")
	}
	base := fmt.Sprintf("/v1/clients/%d/forwards", clientID)
	if len(args) == 2 && args[1] == "list" {
		data, err := call(http.MethodGet, base, nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		var forwards []control.ForwardInfo
		if err := json.Unmarshal(data, &forwards); err != nil {
			return guiCommandResult{}, err
		}
		var out strings.Builder
		for _, forward := range forwards {
			if forward.AgentID == agentID {
				fmt.Fprintf(&out, "%s -> %s\n", forward.Bind, forward.Target)
			}
		}
		if out.Len() == 0 {
			out.WriteString("No forwards for this agent.\n")
		}
		return guiCommandResult{Output: out.String()}, nil
	}
	if len(args) == 4 && args[1] == "add" {
		_, err := call(http.MethodPost, base, map[string]string{"agent_id": agentID, "bind": args[2], "target": args[3]})
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Forward added.\n"}, nil
	}
	if len(args) == 3 && (args[1] == "del" || args[1] == "delete") {
		_, err := call(http.MethodDelete, base+"?agent_id="+url.QueryEscape(agentID)+"&bind="+url.QueryEscape(args[2]), nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: "Forward removed.\n"}, nil
	}
	return guiCommandResult{}, errors.New("use forward add BIND TARGET, forward list, or forward del BIND")
}

func (g *guiServer) agentCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Line string `json:"line"`
	}
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || len(body.Line) > 4096 {
		http.Error(w, "invalid command", http.StatusBadRequest)
		return
	}
	result, err := g.runAgentGUICommand(r.Context(), r.PathValue("id"), body.Line)
	if err != nil {
		guiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "output": result.Output})
		return
	}
	guiJSON(w, http.StatusOK, result)
}
