package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

func TestConsoleSplitsQuotedExecutableArguments(t *testing.T) {
	args, err := splitConsoleCommand(`exec agent-id "/opt/program with spaces" 'one argument' plain`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "agent-id", "/opt/program with spaces", "one argument", "plain"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestConsoleHistoryAndTabCompletion(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	editor.showPrompt("undertow> ", false)
	lines := make(chan string, 2)
	err := editor.read(context.Background(), strings.NewReader("statu\t\n\x1b[A\n"), lines)
	if err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	if first, second := <-lines, <-lines; first != "status" || second != "status" {
		t.Fatalf("history/completion: %q, %q", first, second)
	}
}

func TestConsoleHelpTopics(t *testing.T) {
	var summary, clientSummary, agentSummary, relay, clientRoute bytes.Buffer
	if err := printConsoleHelp(&summary, false, false, true, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SERVER / MAIN MENU", "◆ NAVIGATION\n", "agents", "use NUMBER|ID|HOSTNAME", "◆ SERVER LISTENERS\n", "transports", "logs follow", "clear / cls"} {
		if !strings.Contains(summary.String(), want) {
			t.Fatalf("server main menu missing %q: %q", want, summary.String())
		}
	}
	if strings.Contains(summary.String(), "run-wasm") || strings.Contains(summary.String(), "relay start") {
		t.Fatalf("server main menu exposes agent commands: %q", summary.String())
	}
	if err := printConsoleHelp(&clientSummary, true, false, false, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"VPN CLIENT / MAIN MENU", "internal on|off|status", "vpn on|off|status", "use NUMBER|ID|HOSTNAME", "quit / exit"} {
		if !strings.Contains(clientSummary.String(), want) {
			t.Fatalf("client main menu missing %q: %q", want, clientSummary.String())
		}
	}
	if strings.Contains(clientSummary.String(), "upload") || strings.Contains(clientSummary.String(), "forward") || strings.Contains(clientSummary.String(), "shell") {
		t.Fatalf("client main menu exposes agent commands: %q", clientSummary.String())
	}
	if err := printConsoleHelp(&agentSummary, true, true, false, "", consoleHelpOptions{agent: "TALON"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"VPN CLIENT / AGENT TALON", "AGENT SESSION", "AGENT HOST", "AGENT FILES AND SERVICES", "upload LOCAL REMOTE", "download REMOTE LOCAL", "forward add BIND TARGET", "route accept CIDR", "back"} {
		if !strings.Contains(agentSummary.String(), want) {
			t.Fatalf("selected agent menu missing %q: %q", want, agentSummary.String())
		}
	}
	if !strings.Contains(agentSummary.String(), "\n  upload LOCAL REMOTE") {
		t.Fatalf("selected agent layout is cramped: %q", agentSummary.String())
	}
	if err := printConsoleHelp(&relay, false, true, true, "relay"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"relay start [BIND]", "relay list", "relay stop [BIND]", "--transport relay", "--deny=relay"} {
		if !strings.Contains(relay.String(), want) {
			t.Errorf("relay help missing %q: %q", want, relay.String())
		}
	}
	if err := printConsoleHelp(&clientRoute, true, true, false, "route"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clientRoute.String(), "route accept CIDR") || !strings.Contains(clientRoute.String(), "route add CIDR") {
		t.Fatalf("client route help = %q", clientRoute.String())
	}
}

func TestConsoleHelpColorAndClear(t *testing.T) {
	var plain, colored bytes.Buffer
	if err := printConsoleHelp(&plain, true, true, false, "", consoleHelpOptions{agent: "TALON"}); err != nil {
		t.Fatal(err)
	}
	if err := printConsoleHelp(&colored, true, true, false, "", consoleHelpOptions{agent: "TALON", color: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") || !strings.Contains(colored.String(), "\x1b[1;91mUNDERTOW") || !strings.Contains(colored.String(), "\x1b[1;97m") {
		t.Fatalf("plain=%q colored=%q", plain.String(), colored.String())
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("clear\ncls\nhelp clear\nquit\n"), &output, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "error:") || !strings.Contains(output.String(), "Clear the screen") {
		t.Fatalf("clear/cls output=%q", output.String())
	}
}

func TestConsoleCompletionMatchesMenuLevel(t *testing.T) {
	if got := consoleCompletions(nil, "run", 0, false, true, false); len(got) != 0 {
		t.Fatalf("main menu suggests agent commands: %+v", got)
	}
	if got := consoleCompletions(nil, "run", 0, true, true, false); len(got) < 3 {
		t.Fatalf("selected-agent menu lacks run commands: %+v", got)
	}
	if got := consoleCompletions([]string{"vpn"}, "st", 0, false, true, false); len(got) != 1 || got[0].value != "status" {
		t.Fatalf("VPN subcommand completion: %+v", got)
	}
}

func TestConsoleLocalPathCompletion(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("scripts", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("scripts", "check.sh"), []byte("echo ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tool.wasm", []byte("module"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("input.txt", []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("my scripts", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("my scripts", "quick check.sh"), []byte("echo ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	separator := string(os.PathSeparator)
	for _, test := range []struct {
		input, want string
		selected    bool
	}{
		{"run-script bash scr", "run-script bash scripts" + separator, true},
		{"run-script bash scripts" + separator + "che", "run-script bash scripts" + separator + "check.sh ", true},
		{"run-script agent-1 bash scr", "run-script agent-1 bash scripts" + separator, false},
		{"run-wasm --stdin inp", "run-wasm --stdin input.txt ", true},
		{"run-wasm --background tool.w", "run-wasm --background tool.wasm ", true},
		{"upload scr", "upload scripts" + separator, true},
		{"help rou", "help route", true},
	} {
		t.Run(test.input, func(t *testing.T) {
			editor := newConsoleEditor(io.Discard, true)
			editor.selected = test.selected
			editor.line = []rune(test.input)
			editor.cursor = len(editor.line)
			editor.complete()
			if got := string(editor.line); got != test.want {
				t.Fatalf("completion = %q, want %q", got, test.want)
			}
		})
	}
	serverEditor := newConsoleEditor(io.Discard, false)
	serverEditor.line = []rune("help rel")
	serverEditor.cursor = len(serverEditor.line)
	serverEditor.complete()
	if got := string(serverEditor.line); got != "help relay" {
		t.Fatalf("server help completion = %q", got)
	}
	editor := newConsoleEditor(io.Discard, true)
	editor.selected = true
	editor.line = []rune(`run-script bash "my scr`)
	editor.cursor = len(editor.line)
	editor.complete()
	wantDir := `run-script bash "my scripts` + separator
	if got := string(editor.line); got != wantDir {
		t.Fatalf("quoted directory completion = %q, want %q", got, wantDir)
	}
	editor.line = []rune(wantDir + "quick c")
	editor.cursor = len(editor.line)
	editor.complete()
	editor.complete()
	args, err := splitConsoleCommand(string(editor.line))
	if err != nil || !reflect.DeepEqual(args, []string{"run-script", "bash", "my scripts" + separator + "quick check.sh"}) {
		t.Fatalf("quoted file completion = %q, args=%q, err=%v", string(editor.line), args, err)
	}
}

func TestConsoleTabCompletesScriptDirectoryAndFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("scripts", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("scripts", "check.sh"), []byte("echo ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	editor.selected = true
	lines := make(chan string, 1)
	err := editor.read(context.Background(), strings.NewReader("run-script bash scr\tche\t\n"), lines)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read = %v", err)
	}
	want := "run-script bash scripts" + string(os.PathSeparator) + "check.sh "
	if got := <-lines; got != want {
		t.Fatalf("Tab-completed line = %q, want %q", got, want)
	}
}

func TestConsoleHelpRelayDispatch(t *testing.T) {
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("help relay\nquit\n"), &output, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "relay start [BIND]") || !strings.Contains(output.String(), "--transport relay") {
		t.Fatalf("help relay output = %q", output.String())
	}
}

func TestInteractiveInputPreservesEnterAndDetachesLocally(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	input, detach := editor.beginInteractive()
	defer editor.endInteractive()
	if err := editor.read(context.Background(), strings.NewReader("whoami\r\x1d"), make(chan string, 1)); err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	var forwarded []byte
	for len(input) > 0 {
		forwarded = append(forwarded, <-input)
	}
	if string(forwarded) != "whoami\r" {
		t.Fatalf("forwarded input = %q", forwarded)
	}
	select {
	case <-detach:
	default:
		t.Fatal("Ctrl-] did not detach the shell")
	}
}

func TestInteractiveCtrlCNeedsConfirmation(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	editor.showPrompt("undertow> ", false)
	lines := make(chan string, 1)
	if err := editor.read(context.Background(), strings.NewReader("stat\x03nus\n"), lines); err != io.EOF {
		t.Fatalf("declined Ctrl+C = %v", err)
	}
	if line := <-lines; line != "status" {
		t.Fatalf("declined Ctrl+C lost command: %q", line)
	}
	if !strings.Contains(output.String(), "Stop VPN and remove its routes? [y/N]") {
		t.Fatal("confirmation prompt missing")
	}
	lines = make(chan string, 1)
	if err := editor.read(context.Background(), strings.NewReader("\x03y"), lines); err != nil {
		t.Fatalf("confirmed Ctrl+C = %v", err)
	}
	if line := <-lines; line != "quit" {
		t.Fatalf("confirmed Ctrl+C = %q", line)
	}
}

func TestBackgroundCommandDetachesWithoutStoppingVPN(t *testing.T) {
	stopped := false
	caller := func(context.Context, string, string, any) ([]byte, error) {
		return []byte(`{"agents":[]}`), nil
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("background\n"), &output, caller, func() uint64 { return 9 }, func() { stopped = true }, nil, nil); err != nil {
		t.Fatal(err)
	}
	if stopped || !strings.Contains(output.String(), "VPN continues") {
		t.Fatalf("background stopped VPN or failed to report detach: %q", output.String())
	}
}

func TestServerConsoleDetachAndStopAreDistinct(t *testing.T) {
	for _, command := range []string{"background", "quit", "exit"} {
		t.Run(command, func(t *testing.T) {
			stopped := false
			var output bytes.Buffer
			err := runConsole(context.Background(), strings.NewReader(command+"\n"), &output, nil, nil, nil, nil, nil, consoleFeatures{
				serverAttached: true,
				stopServer:     func() error { stopped = true; return nil },
			})
			if err != nil || stopped || !strings.Contains(output.String(), "Server continues") {
				t.Fatalf("command=%q err=%v stopped=%v output=%s", command, err, stopped, output.String())
			}
		})
	}
	stopped := false
	var output bytes.Buffer
	err := runConsole(context.Background(), strings.NewReader("stop\n"), &output, nil, nil, nil, nil, nil, consoleFeatures{
		serverAttached: true,
		stopServer:     func() error { stopped = true; return nil },
	})
	if err != nil || !stopped || !strings.Contains(output.String(), "Server stopped") {
		t.Fatalf("stop err=%v stopped=%v output=%s", err, stopped, output.String())
	}
}

func TestServerConsoleLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := showServerLogs(context.Background(), &output, path, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "first\nsecond") {
		t.Fatalf("logs output=%q", output.String())
	}
}

func TestServerConsoleLogsFollowUntilInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			_, _ = file.WriteString("new event\n")
			_ = file.Close()
		}
		time.Sleep(500 * time.Millisecond)
		lines <- ""
	}()
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := showServerLogs(ctx, &output, path, true, lines); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "new event") {
		t.Fatalf("follow missed appended line: %q", output.String())
	}
}

func TestInteractiveServerAgentContext(t *testing.T) {
	var calls []string
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		calls = append(calls, method+" "+path)
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host", VirtualIP: "172.16.254.2"}}})
		}
		if strings.HasSuffix(path, "/exec") {
			return json.Marshal(pivot.ExecResult{Stdout: "1000\n"})
		}
		return nil, nil
	}
	input := strings.NewReader("agents\nuse 1\nhelp\nhelp exec\nexec /usr/bin/id -u\nroute add 10.10.0.0/16\nback\nquit\n")
	var output bytes.Buffer
	if err := runConsole(context.Background(), input, &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pivot-host", "undertow[pivot-host]>", "exec PROGRAM", "1000", "Route added."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %q", want, output.String())
		}
	}
	for _, want := range []string{"POST /v1/agents/agent-long-id/exec", "POST /v1/routes"} {
		if !containsCall(calls, want) {
			t.Fatalf("missing call %q in %v", want, calls)
		}
	}
}

func TestInteractiveClientAgentContext(t *testing.T) {
	var routeArgs []string
	caller := func(_ context.Context, _, path string, _ any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		return nil, nil
	}
	route := func(_ context.Context, args []string, _ io.Writer) error {
		routeArgs = append([]string(nil), args...)
		return nil
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nroute add 10.10.0.0/16\nquit\n"), &output, caller, func() uint64 { return 704 }, nil, route, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(routeArgs, []string{"route", "add", "10.10.0.0/16", "agent-long-id"}) {
		t.Fatalf("route args=%q", routeArgs)
	}
}

func TestSelectedAgentStartsBackgroundJob(t *testing.T) {
	var requests []string
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		requests = append(requests, method+" "+path)
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "agent-host"}}})
		}
		if path == "/v1/agents/agent-a/jobs" {
			return json.Marshal(control.JobInfo{ID: "job-1", AgentID: "agent-a", State: "running"})
		}
		if path == "/v1/jobs?agent_id=agent-a" {
			return json.Marshal([]control.JobInfo{{ID: "job-1", AgentID: "agent-a", State: "running"}})
		}
		if path == "/v1/jobs/job-1" {
			return json.Marshal(control.JobInfo{ID: "job-1", AgentID: "agent-a", State: "completed"})
		}
		if path == "/v1/jobs/job-1/output" {
			return json.Marshal(control.JobInfo{ID: "job-1", AgentID: "agent-a", Output: "hello from job\n"})
		}
		return nil, fmt.Errorf("unexpected request %s", path)
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\njob start powershell.exe -NoProfile\njobs\njobs 1\njobs show 1\njob output 1\njobs output job-1\nhelp show 1\nquit\n"), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Job job-1 started") || !strings.Contains(output.String(), "Jobs (1):\n  1  job-1") || strings.Count(output.String(), "hello from job") != 2 || !strings.Contains(output.String(), "To inspect job 1, use job show 1") || strings.Contains(output.String(), "error:") {
		t.Fatalf("output=%s", output.String())
	}
	if !slices.Contains(requests, "POST /v1/agents/agent-a/jobs") || !slices.Contains(requests, "GET /v1/jobs?agent_id=agent-a") || !slices.Contains(requests, "GET /v1/jobs/job-1") || !slices.Contains(requests, "GET /v1/jobs/job-1/output") {
		t.Fatalf("requests=%v", requests)
	}
}

func TestJobNumbersReferToLastPrintedList(t *testing.T) {
	listCalls := 0
	caller := func(_ context.Context, _, path string, _ any) ([]byte, error) {
		switch path {
		case "/v1/jobs":
			listCalls++
			if listCalls == 1 {
				return json.Marshal([]control.JobInfo{{ID: "older", State: "completed"}})
			}
			return json.Marshal([]control.JobInfo{{ID: "newer", State: "running"}, {ID: "older", State: "completed"}})
		case "/v1/jobs/older":
			return json.Marshal(control.JobInfo{ID: "older", State: "completed"})
		default:
			return nil, fmt.Errorf("unexpected request %s", path)
		}
	}
	var output bytes.Buffer
	selection := new(consoleJobSelection)
	for _, args := range [][]string{{"jobs"}, {"jobs", "1"}, {"job", "show", "1"}} {
		if err := runConsoleJobCommand(context.Background(), &output, caller, args, "", selection); err != nil {
			t.Fatal(err)
		}
	}
	if listCalls != 1 || strings.Count(output.String(), "Job older") != 2 {
		t.Fatalf("list calls=%d output=%s", listCalls, output.String())
	}
}

func TestFilteredJobNumbersResolveFromPrintedList(t *testing.T) {
	caller := func(_ context.Context, _, path string, _ any) ([]byte, error) {
		switch path {
		case "/v1/jobs?agent_id=agent-a":
			return json.Marshal([]control.JobInfo{{ID: "agent-a-job", AgentID: "agent-a"}})
		case "/v1/jobs/agent-a-job":
			return json.Marshal(control.JobInfo{ID: "agent-a-job", AgentID: "agent-a"})
		default:
			return nil, fmt.Errorf("unexpected request %s", path)
		}
	}
	var output bytes.Buffer
	selection := new(consoleJobSelection)
	if err := runConsoleJobCommand(context.Background(), &output, caller, []string{"jobs", "agent-a"}, "", selection); err != nil {
		t.Fatal(err)
	}
	if err := runConsoleJobCommand(context.Background(), &output, caller, []string{"jobs", "1"}, "", selection); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Job agent-a-job") {
		t.Fatalf("output=%s", output.String())
	}
}

func TestSelectedAgentStartsMemoryScriptJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(path, []byte("echo script\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seen := false
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a"}}})
		}
		if method == "POST" && route == "/v1/agents/agent-a/scripts/jobs" {
			encoded, _ := json.Marshal(body)
			var request struct {
				Language string `json:"language"`
				Source   []byte `json:"source"`
			}
			if json.Unmarshal(encoded, &request) != nil || request.Language != "bash" || string(request.Source) != "echo script\n" {
				t.Fatalf("script request=%s", encoded)
			}
			seen = true
			return json.Marshal(control.JobInfo{ID: "script-job", AgentID: "agent-a", Kind: "script"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nrun-script --background bash \""+path+"\"\nquit\n"), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !seen || !strings.Contains(output.String(), "Script job script-job started") {
		t.Fatalf("request seen=%t output=%s", seen, output.String())
	}
}

func TestSelectedAgentStartsMemoryWASMJob(t *testing.T) {
	module, err := os.ReadFile(filepath.Join("..", "..", "internal", "pivot", "testdata", "wasm_args.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tool.wasm")
	if err := os.WriteFile(path, module, 0600); err != nil {
		t.Fatal(err)
	}
	seen := false
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a"}}})
		}
		if method == "POST" && route == "/v1/agents/agent-a/wasm/jobs" {
			encoded, _ := json.Marshal(body)
			var request struct {
				Source []byte   `json:"source"`
				Args   []string `json:"args"`
			}
			if json.Unmarshal(encoded, &request) != nil || !bytes.Equal(request.Source, module) || !reflect.DeepEqual(request.Args, []string{"audit"}) {
				t.Fatalf("WASM request=%s", encoded)
			}
			seen = true
			return json.Marshal(control.JobInfo{ID: "wasm-job", AgentID: "agent-a", Kind: "wasm"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nrun-wasm --background \""+path+"\" audit\nquit\n"), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !seen || !strings.Contains(output.String(), "WASM job wasm-job started") {
		t.Fatalf("request seen=%t output=%s", seen, output.String())
	}
}

func TestSelectedAgentShowUsesCurrentStatus(t *testing.T) {
	caller := func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method != "GET" || path != "/v1/status" {
			return nil, fmt.Errorf("unexpected %s %s", method, path)
		}
		return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "pivot-host", OS: "linux", Arch: "amd64", VirtualIP: "172.16.254.2", Routes: []control.NetworkRoute{{Prefix: "10.20.0.0/16", Gateway: "192.168.50.1"}}}}})
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nshow\nquit\n"), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Agent agent-a") || !strings.Contains(output.String(), "10.20.0.0/16") {
		t.Fatalf("agent show=%s", output.String())
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestConsoleExecSendsArgvWithoutShell(t *testing.T) {
	var request pivot.ExecRequest
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		if method != http.MethodPost || path != "/v1/agents/agent-id/exec" {
			t.Fatalf("request = %s %s", method, path)
		}
		encoded, _ := json.Marshal(body)
		if err := json.Unmarshal(encoded, &request); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(pivot.ExecResult{Stdout: "done\n", ExitCode: 0})
	}
	var output bytes.Buffer
	if err := runConsoleCommand(context.Background(), &output, caller, false, 0, nil, []string{"exec", "agent-id", "/usr/bin/id", "-u"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Argv, []string{"/usr/bin/id", "-u"}) || output.String() != "done\n[exit 0]\n" {
		t.Fatalf("request=%+v output=%q", request, output.String())
	}
}

func TestConsoleSelectedAgentBuiltin(t *testing.T) {
	var request pivot.ExecRequest
	caller := func(_ context.Context, _, path string, body any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		if path != "/v1/agents/agent-long-id/exec" {
			t.Fatalf("unexpected path %q", path)
		}
		encoded, _ := json.Marshal(body)
		if err := json.Unmarshal(encoded, &request); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(pivot.ExecResult{Stdout: "file.txt\n"})
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nls /tmp\nquit\n"), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if request.Builtin != "ls" || !reflect.DeepEqual(request.Args, []string{"/tmp"}) || !strings.Contains(output.String(), "file.txt") {
		t.Fatalf("request=%+v output=%q", request, output.String())
	}
}

func TestConsoleSelectedAgentForward(t *testing.T) {
	var request map[string]string
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		if method != http.MethodPost || path != "/v1/clients/704/forwards" {
			t.Fatalf("unexpected forward call %s %s", method, path)
		}
		encoded, _ := json.Marshal(body)
		if err := json.Unmarshal(encoded, &request); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(control.ForwardInfo{AgentID: request["agent_id"], Bind: request["bind"], Target: request["target"]})
	}
	var output bytes.Buffer
	input := strings.NewReader("use 1\nforward add 0.0.0.0:8080 127.0.0.1:8080\nquit\n")
	if err := runConsole(context.Background(), input, &output, caller, func() uint64 { return 704 }, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if request["agent_id"] != "agent-long-id" || request["bind"] != "0.0.0.0:8080" || request["target"] != "127.0.0.1:8080" {
		t.Fatalf("wrong agent forward: %+v", request)
	}
}

func TestVPNConsoleCannotChangeServerRoutes(t *testing.T) {
	called := false
	caller := func(context.Context, string, string, any) ([]byte, error) { called = true; return nil, nil }
	for _, args := range [][]string{{"select", "agent-id"}} {
		if err := runConsoleCommand(context.Background(), &bytes.Buffer{}, caller, true, 704, nil, args); err == nil {
			t.Fatalf("VPN client accepted %q", args)
		}
	}
	if called {
		t.Fatal("VPN client sent an operator request")
	}
}

func TestInteractiveClientUploadUsesSelectedAgentAndAbsoluteLocalPath(t *testing.T) {
	var request clientFileRequest
	caller := func(_ context.Context, _, path string, body any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		if path != "/v1/file/transfer" {
			t.Fatalf("unexpected path %s", path)
		}
		request = body.(clientFileRequest)
		return json.Marshal(pivot.FileMessage{OK: true, Size: 5, SHA256: "digest"})
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nupload ./source.bin /tmp/remote.bin\nquit\n"), &output, caller, func() uint64 { return 704 }, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if request.AgentID != "agent-long-id" || request.Operation != "upload" || !filepath.IsAbs(request.LocalPath) || request.RemotePath != "/tmp/remote.bin" {
		t.Fatalf("wrong transfer request: %+v", request)
	}
	if !strings.Contains(output.String(), "Upload complete: 5 bytes") {
		t.Fatalf("missing transfer result: %q", output.String())
	}
}
