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
	"path/filepath"
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

func (g *guiServer) agentGUIHelp() string {
	var out strings.Builder
	m := consoleMenu{out: &out, color: false}
	m.title("GUI CLIENT", "AGENT COMMANDS")
	section := func(title string, commands [][2]string) {
		m.section(title)
		for _, item := range commands {
			m.row(item[0], item[1])
		}
	}
	section("AGENT SESSION", [][2]string{{"show", "Inspect this agent"}, {"agent events", "Recent lifecycle events"}, {"agent sleep [SECONDS JITTER]", "View or set idle sleep"}, {"agent shutdown", "Ask this agent to exit"}, {"session kill", "Close this session; agent may reconnect"}, {"shell", "Open the separate live shell panel"}})
	section("HOST", [][2]string{{"pwd; ls [PATH]; stat PATH", "Browse this agent's files"}, {"mkdir PATH; rm PATH", "Create or remove a path"}, {"whoami; ps; privileges", "Identity, processes and privileges"}, {"env [NAME]", "Environment variables"}, {"interfaces; dns; route-table", "Network configuration"}, {"screens; screenshot [NUMBER]", "List screens or capture explicitly"}})
	section("FILES AND SERVICES", [][2]string{{"upload LOCAL REMOTE", "Send a client file to this agent"}, {"download REMOTE [LOCAL]", "Save an agent file on this client"}, {"forward add BIND TARGET", "Expose a client service through this agent"}, {"forward list; forward del BIND", "Inspect or close forwards"}, {"relay start [BIND]", "Start a relay listener on this agent"}, {"relay list; relay stop BIND", "Inspect or close relay listeners"}})
	section("EXECUTION AND JOBS", [][2]string{{"exec PROGRAM [ARGS]", "Run one program when requested"}, {"job start PROGRAM [ARGS]", "Start a background job"}, {"jobs; job show|output ID", "Inspect retained jobs and output"}, {"job cancel|stop|delete ID", "Manage a job"}, {"run-script [OPTIONS] FILE", "Run a client-side script file"}, {"run-wasm|run-native|run-assembly|run-bof ...", "Run a client-side module file"}})
	section("JUMP", [][2]string{{"jumps", "List Jump records from this agent"}, {"jump create TARGET ARTIFACT METHOD CONTEXT", "Record Jump intent"}, {"jump show|prepare ID", "Inspect or prepare a Jump"}, {"jump start ID [PATH]", "Stream and start the linked Windows method Job"}})
	section("CLIENT ROUTING", [][2]string{{"routes", "Show routes for this agent"}, {"route accept CIDR", "Accept an advertised route on this client"}, {"route add CIDR", "Add a custom route on this client"}, {"route del CIDR", "Remove this client's accepted route"}})
	section("MODULE BANK", [][2]string{{"modules; bofs", "List loaded commands"}, {"help MODULE", "Show a loaded command's full help"}, {"load module|wasm|assembly|bof FILE [NAME]", "Register a client module"}, {"unload module|wasm|assembly|bof NAME", "Remove a loaded command"}, {"MODULE [ARGS] [--background]", "Run a loaded command on this agent"}})
	if g.modules != nil {
		for _, kind := range []struct{ key, title string }{{"bof", "LOADED BOFS"}, {"module", "LOADED NATIVE MODULES"}, {"wasm", "LOADED WASM MODULES"}, {"assembly", "LOADED .NET ASSEMBLIES"}} {
			var items [][2]string
			for _, module := range g.modules.list() {
				if module.Kind == kind.key {
					items = append(items, [2]string{module.Name, module.Description})
				}
			}
			if len(items) > 0 {
				section(kind.title, items)
			}
		}
	}
	m.hint("Commands are bound to this agent. Local paths refer to the Undertow client host.")
	m.hint("Opening the workspace never starts an agent operation.")
	return out.String()
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
		return guiCommandResult{}, errors.New("use load module|wasm|assembly|bof FILE [NAME] [--format FORMAT]")
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
	if len(args) != 3 || args[1] != "bof" && args[1] != "module" && args[1] != "wasm" && args[1] != "assembly" {
		return guiCommandResult{}, errors.New("use unload module|wasm|bof|assembly NAME")
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
	case "jump", "jumps", "deploy", "deployments":
		var out strings.Builder
		remoteCall := func(_ context.Context, method, path string, body any) ([]byte, error) {
			return call(method, path, body)
		}
		if err := runConsoleDeployment(ctx, &out, remoteCall, args, agentID); err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: out.String()}, nil
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
		return guiCommandResult{Output: g.agentGUIHelp()}, nil
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
		data, err := call(http.MethodPost, base+"/exec", pivot.ExecRequest{Builtin: "screens"})
		if err != nil {
			return guiCommandResult{}, err
		}
		var response pivot.ExecResult
		if err := json.Unmarshal(data, &response); err != nil {
			return guiCommandResult{}, err
		}
		if response.QueuedJobID != "" {
			return guiCommandResult{}, errors.New("server returned a background job for a foreground screen list; update the server")
		}
		if response.Error != "" {
			return guiCommandResult{}, errors.New(response.Error)
		}
		screens := response.Screens
		if screens == nil {
			_ = json.Unmarshal([]byte(response.Stdout), &screens)
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
			data, err := call(http.MethodPost, base+"/exec", pivot.ExecRequest{Builtin: "screens"})
			if err != nil {
				return guiCommandResult{}, err
			}
			var response pivot.ExecResult
			if err := json.Unmarshal(data, &response); err != nil {
				return guiCommandResult{}, err
			}
			if response.QueuedJobID != "" {
				return guiCommandResult{}, errors.New("server returned a background job for a foreground screen list; update the server")
			}
			if response.Error != "" {
				return guiCommandResult{}, errors.New(response.Error)
			}
			screens := response.Screens
			if screens == nil {
				_ = json.Unmarshal([]byte(response.Stdout), &screens)
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
			var item struct {
				ID     string `json:"id"`
				Size   int64  `json:"size"`
				SHA256 string `json:"sha256"`
				State  string `json:"state"`
			}
			if err := json.Unmarshal(data, &item); err != nil {
				return guiCommandResult{Output: out.String()}, err
			}
			if item.State == "queued" || item.State == "dispatching" {
				return guiCommandResult{Output: out.String()}, errors.New("server returned a background job for a foreground screenshot; update the server")
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
		if result.QueuedJobID != "" {
			return guiCommandResult{}, errors.New("server returned a background job for a foreground command; update the server")
		}
		if result.Error != "" {
			return guiCommandResult{Output: result.Stdout + result.Stderr}, errors.New(result.Error)
		}
		return guiCommandResult{Output: result.Stdout + result.Stderr}, nil
	case "upload", "download":
		if len(args) != 3 && !(args[0] == "download" && len(args) == 2) {
			return guiCommandResult{}, errors.New("use upload LOCAL REMOTE or download REMOTE [LOCAL]")
		}
		request := clientFileRequest{AgentID: agentID, Operation: args[0]}
		if args[0] == "upload" {
			request.LocalPath, request.RemotePath = args[1], args[2]
		} else {
			request.RemotePath = args[1]
			if len(args) == 3 {
				request.LocalPath = args[2]
			} else {
				request.LocalPath, err = defaultDownloadPath(agentID, request.RemotePath)
				if err != nil {
					return guiCommandResult{}, err
				}
				if err = prepareClientOutputDirectory(filepath.Dir(request.LocalPath)); err != nil {
					return guiCommandResult{}, err
				}
			}
		}
		request.LocalPath, err = filepath.Abs(request.LocalPath)
		if err != nil {
			return guiCommandResult{}, err
		}
		result, err := g.client.transferProgress(ctx, mustGUIJSON(request), nil)
		if err != nil {
			return guiCommandResult{}, err
		}
		return guiCommandResult{Output: fmt.Sprintf("%s complete: %d bytes, SHA-256 %s\n", strings.Title(args[0]), result.Size, result.SHA256)}, nil
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
		if result.QueuedJobID != "" {
			return guiCommandResult{}, errors.New("server returned a background job for a foreground command; update the server")
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
		if len(args) >= 2 && args[1] == "sleep" {
			if len(args) == 2 {
				data, err := call(http.MethodGet, base, nil)
				if err != nil {
					return guiCommandResult{}, err
				}
				var agent control.AgentInfo
				if err := json.Unmarshal(data, &agent); err != nil {
					return guiCommandResult{}, err
				}
				if !agent.SleepSupported {
					return guiCommandResult{Output: "Idle sleep is unavailable for this agent build; rebuild its payload.\n"}, nil
				}
				return guiCommandResult{Output: fmt.Sprintf("Idle sleep: %d seconds, %d%% jitter.\n", agent.Sleep.IntervalSeconds, agent.Sleep.JitterPercent)}, nil
			}
			if len(args) != 4 {
				return guiCommandResult{}, errors.New("use agent sleep [SECONDS JITTER]")
			}
			seconds, err := strconv.Atoi(args[2])
			if err != nil {
				return guiCommandResult{}, err
			}
			jitter, err := strconv.Atoi(args[3])
			if err != nil {
				return guiCommandResult{}, err
			}
			policy := control.SleepPolicy{IntervalSeconds: seconds, JitterPercent: jitter}
			if err := policy.Validate(); err != nil {
				return guiCommandResult{}, err
			}
			if _, err := call(http.MethodPut, base+"/sleep", policy); err != nil {
				return guiCommandResult{}, err
			}
			return guiCommandResult{Output: fmt.Sprintf("Idle sleep set to %d seconds, %d%% jitter.\n", seconds, jitter)}, nil
		}
		if len(args) == 2 && args[1] == "shutdown" {
			data, err := call(http.MethodPost, base+"/shutdown", map[string]any{})
			if err != nil {
				return guiCommandResult{}, err
			}
			if id := queuedLifecycleID(data); id != "" {
				return guiCommandResult{Output: "Agent shutdown queued for its next check-in as job " + id + ".\n"}, nil
			}
			return guiCommandResult{Output: "Agent shutdown requested. The packaged agent will exit.\n"}, nil
		}
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
		return guiCommandResult{}, errors.New("use agent events, agent sleep, or agent shutdown")
	case "session":
		if len(args) != 2 || args[1] != "kill" {
			return guiCommandResult{}, errors.New("use session kill")
		}
		data, err := call(http.MethodPost, "/v1/sessions/"+url.PathEscape(agentID)+"/kill", map[string]any{})
		if err != nil {
			return guiCommandResult{}, err
		}
		if id := queuedLifecycleID(data); id != "" {
			return guiCommandResult{Output: "Session kill queued for the next check-in as job " + id + ".\n"}, nil
		}
		return guiCommandResult{Output: "Agent session closed. The agent may reconnect.\n"}, nil
	case "relay":
		return g.runAgentGUIRelay(ctx, call, agentID, args)
	case "route":
		return g.runAgentGUIRoute(ctx, agentID, args)
	case "routes":
		if len(args) != 1 {
			return guiCommandResult{}, errors.New("use routes")
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
		var agent control.AgentInfo
		for _, candidate := range status.Agents {
			if candidate.ID == agentID {
				agent = candidate
				break
			}
		}
		if agent.ID == "" {
			return guiCommandResult{}, errors.New("agent is not connected")
		}
		var out strings.Builder
		out.WriteString("Advertised by this agent:\n")
		if len(agent.AdvertisedRoutes) == 0 {
			out.WriteString("  (none)\n")
		}
		for _, prefix := range agent.AdvertisedRoutes {
			fmt.Fprintf(&out, "  %s\n", prefix)
		}
		out.WriteString("Accepted on this client:\n")
		g.client.routeMu.Lock()
		count := 0
		for _, route := range g.client.routes {
			if route.AgentID == agentID {
				fmt.Fprintf(&out, "  %s (%s)\n", route.Prefix, map[bool]string{true: "installed", false: "pending"}[g.client.active[route.Prefix]])
				count++
			}
		}
		g.client.routeMu.Unlock()
		if count == 0 {
			out.WriteString("  (none)\n")
		}
		return guiCommandResult{Output: out.String()}, nil
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
				out.WriteString(relay.Bind + " (" + relay.State + ")\n")
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
		if relay.State == "pending" {
			return guiCommandResult{Output: "Relay " + relay.Bind + " queued for the next check-in.\n"}, nil
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
				fmt.Fprintf(&out, "%s -> %s (%s)\n", forward.Bind, forward.Target, forward.State)
			}
		}
		if out.Len() == 0 {
			out.WriteString("No forwards for this agent.\n")
		}
		return guiCommandResult{Output: out.String()}, nil
	}
	if len(args) == 4 && args[1] == "add" {
		data, err := call(http.MethodPost, base, map[string]string{"agent_id": agentID, "bind": args[2], "target": args[3]})
		if err != nil {
			return guiCommandResult{}, err
		}
		var forward control.ForwardInfo
		if err := json.Unmarshal(data, &forward); err != nil {
			return guiCommandResult{}, err
		}
		if forward.State == "pending" {
			return guiCommandResult{Output: "Forward queued for the next check-in.\n"}, nil
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
