package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/control"
)

func copyArtifactFixture(t *testing.T, source, name string) string {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadedNativeAndWASMRegistry(t *testing.T) {
	bofs := newLoadedBOFRegistry()
	artifacts := newLoadedArtifactRegistry()
	nativePath := copyArtifactFixture(t, filepath.Join("..", "..", "modules", "native", "wininfo", "wininfo.module"), "wininfo.module")
	wasmPath := copyArtifactFixture(t, filepath.Join("..", "..", "modules", "wasm", "triage", "triage.wasm"), "triage.wasm")
	if err := os.WriteFile(wasmPath+".json", []byte(`{"description":"Triage a host","usage":"triage [path]","help":"Portable host triage."}`), 0600); err != nil {
		t.Fatal(err)
	}
	native, err := artifacts.load("module", nativePath, "", bofs)
	if err != nil || native.Name != "wininfo" || native.OS != "windows" {
		t.Fatalf("native=%+v %v", native, err)
	}
	wasm, err := artifacts.load("wasm", wasmPath, "triage", bofs)
	if err != nil || wasm.Name != "triage" || wasm.Help.Description != "Triage a host" {
		t.Fatalf("wasm=%+v %v", wasm, err)
	}
	if _, err := artifacts.load("wasm", wasmPath, "wininfo", bofs); err == nil {
		t.Fatal("duplicate alias accepted")
	}
	if _, err := artifacts.load("module", nativePath, "help", bofs); err == nil {
		t.Fatal("built-in alias accepted")
	}
	if _, err := bofs.load(copyBOFFixture(t, "hello"), "triage", "", false, artifacts); err == nil {
		t.Fatal("BOF shadowed WASM")
	}
	if err := os.Remove(wasmPath); err != nil {
		t.Fatal(err)
	}
	if len(wasm.Data) == 0 {
		t.Fatal("loaded WASM lost bytes after source deletion")
	}
	var help strings.Builder
	if err := printConsoleHelp(&help, false, false, false, "triage", consoleHelpOptions{loadedArtifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Triage a host", "Portable host triage", "portable WASM"} {
		if !strings.Contains(help.String(), part) {
			t.Fatalf("help missing %q: %s", part, help.String())
		}
	}
	for _, tc := range []struct {
		before       []string
		prefix, want string
	}{
		{nil, "tria", "triage"},
		{[]string{"help"}, "tria", "triage"},
		{[]string{"unload", "wasm"}, "tria", "triage"},
		{[]string{"load"}, "mo", "module"},
	} {
		matches := consoleCompletions(tc.before, tc.prefix, 0, false, false, false, bofs, artifacts)
		if len(matches) != 1 || matches[0].value != tc.want {
			t.Fatalf("completion %+v: %+v", tc, matches)
		}
	}
	if err := artifacts.unload("wasm", "triage"); err != nil {
		t.Fatal(err)
	}
	if artifacts.get("triage") != nil {
		t.Fatal("unloaded WASM remains")
	}
}

func TestLoadedArtifactsConsoleJobs(t *testing.T) {
	nativePath := copyArtifactFixture(t, filepath.Join("..", "..", "modules", "native", "wininfo", "wininfo.module"), "wininfo.module")
	wasmPath := copyArtifactFixture(t, filepath.Join("..", "..", "modules", "wasm", "triage", "triage.wasm"), "triage.wasm")
	nativeBytes, _ := os.ReadFile(nativePath)
	wasmBytes, _ := os.ReadFile(wasmPath)
	var routes []string
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "TALON"}}})
		}
		if method == "POST" && (strings.HasSuffix(route, "/native/jobs") || strings.HasSuffix(route, "/wasm/jobs")) {
			request, _ := json.Marshal(body)
			var parsed struct {
				Source []byte
				Args   []string
			}
			if err := json.Unmarshal(request, &parsed); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(route, "/native/jobs") && !bytes.Equal(parsed.Source, nativeBytes) {
				t.Fatal("native bytes changed")
			}
			if strings.HasSuffix(route, "/wasm/jobs") && !bytes.Equal(parsed.Source, wasmBytes) {
				t.Fatal("WASM bytes changed")
			}
			routes = append(routes, route)
			return json.Marshal(control.JobInfo{ID: fmt.Sprintf("job-%d", len(routes)), AgentID: "agent-a"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var output strings.Builder
	commands := fmt.Sprintf("load module %q wininfo\nload wasm %q triage\nmodules\nhelp triage\nuse 1\nwininfo --background\ntriage --background audit\nunload wasm triage\nquit\n", nativePath, wasmPath)
	if err := runConsole(context.Background(), strings.NewReader(commands), &output, caller, nil, nil, nil, nil, consoleFeatures{}); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0] != "/v1/agents/agent-a/native/jobs" || routes[1] != "/v1/agents/agent-a/wasm/jobs" {
		t.Fatalf("routes=%v output=%s", routes, output.String())
	}
	for _, part := range []string{"Loaded module: wininfo", "Loaded wasm: triage", "Native job job-1 started on TALON", "WASM job job-2 started on TALON", "Unloaded wasm: triage"} {
		if !strings.Contains(output.String(), part) {
			t.Fatalf("missing %q in %s", part, output.String())
		}
	}
}

func TestModuleBankPreload(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct{ kind, source, filename string }{
		{"module", filepath.Join("..", "..", "modules", "native", "wininfo", "wininfo.module"), "wininfo.module"},
		{"wasm", filepath.Join("..", "..", "modules", "wasm", "triage", "triage.wasm"), "triage.wasm"},
		{"bof", filepath.Join("..", "..", "modules", "bof", "Winver.x64.o"), "Winver.x64.o"},
	} {
		dir := filepath.Join(root, item.kind)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(item.source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, item.filename), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("UNDERTOW_MODULES_DIR", root)
	artifacts, bofs := newLoadedArtifactRegistry(), newLoadedBOFRegistry()
	if problems := preloadModuleBank(artifacts, bofs); len(problems) != 0 {
		t.Fatalf("preload errors: %v", problems)
	}
	if bofs.get("bof-winver") == nil || artifacts.get("module-wininfo") == nil || artifacts.get("wasm-triage") == nil {
		t.Fatalf("preloaded bofs=%v modules=%v", bofs.names(), artifacts.names())
	}
	var output strings.Builder
	if err := printConsoleHelp(&output, false, false, false, "", consoleHelpOptions{loadedBOFs: bofs, loadedArtifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Loaded BOFs") || !strings.Contains(output.String(), "Loaded Native Modules") || !strings.Contains(output.String(), "Loaded WASM Modules") {
		t.Fatalf("bank help: %s", output.String())
	}
	if len(newLoadedArtifactRegistry().names()) != 0 {
		t.Fatal("new registry inherited session entries")
	}
}

func TestPackagedModuleBank(t *testing.T) {
	t.Setenv("UNDERTOW_MODULES_DIR", filepath.Join("..", "..", "modules"))
	artifacts, bofs := newLoadedArtifactRegistry(), newLoadedBOFRegistry()
	if problems := preloadModuleBank(artifacts, bofs); len(problems) != 0 {
		t.Fatalf("packaged bank has invalid artifacts: %v", problems)
	}
	if len(bofs.names()) != 34 || len(artifacts.names()) < 11 {
		t.Fatalf("packaged bank: bofs=%v modules=%v", bofs.names(), artifacts.names())
	}
	for _, name := range bofs.names() {
		manifest := bofs.get(name).Manifest
		if manifest.Description == "" || manifest.Usage == "" || manifest.Help == "" {
			t.Errorf("BOF %s is missing help", name)
		}
	}
	for _, name := range []string{"bof-ai-surface", "bof-asktgt", "bof-dcsync-single", "bof-kiwi", "bof-lsadump-sam", "bof-petitpotam", "bof-winver"} {
		if bofs.get(name) == nil {
			t.Errorf("missing packaged BOF %s", name)
			continue
		}
		if bofs.get(name).Manifest.Description == "" {
			t.Errorf("BOF %s is missing help", name)
		}
	}
	for _, name := range []string{"module-askpass", "module-hello", "module-hostcheck", "module-sift", "module-wininfo", "wasm-artifact-discovery", "wasm-enterprise-posture", "wasm-inventory", "wasm-persistence-audit", "wasm-privilege-audit", "wasm-triage"} {
		entry := artifacts.get(name)
		if entry == nil {
			t.Errorf("missing packaged module %s", name)
			continue
		}
		if entry.Help.Description == "" || entry.Help.Help == "" || entry.Help.Usage == "" {
			t.Errorf("module %s is missing help", name)
		}
	}
}
