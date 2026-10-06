//go:build linux || windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agentpkg "undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/control"
)

func deploymentArtifactStore(t *testing.T) (*agentprofile.Store, agentprofile.Artifact) {
	t.Helper()
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	const templateName = "undertow-agent-windows-amd64.exe"
	if err := os.WriteFile(filepath.Join(templates, templateName), []byte("PE-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := agentprofile.WriteTemplateManifest(templates, "test", []string{templateName}); err != nil {
		t.Fatal(err)
	}
	store, err := agentprofile.OpenStore(filepath.Join(dir, "store"), templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("deploy", agentpkg.Config{Version: agentpkg.ConfigVersion, Server: "127.0.0.1:443", Transport: "websocket", Fingerprint: strings.Repeat("a", 64), AuthMode: "none", Credential: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Build("deploy", "windows", "amd64", "agent.exe")
	if err != nil {
		t.Fatal(err)
	}
	return store, artifact
}

func TestWindowsDeploymentPreflightRevalidatesArtifactAndSelectedHost(t *testing.T) {
	store, artifact := deploymentArtifactStore(t)
	distribution := &agentDistribution{store: store, agentHosts: map[string]*agentPayloadHost{}}
	executor := &windowsDeploymentExecutor{distribution: distribution}
	record := control.DeploymentRecord{ID: "deployment-one", SourceAgentID: "source", ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Method: "winrm", Context: "current-user"}
	if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: "direct-share"}); err != nil {
		t.Fatalf("direct-share preflight: %v", err)
	}
	distribution.agentHosts["host-one"] = &agentPayloadHost{info: agentPayloadHostInfo{ID: "host-one", AgentID: "source", ArtifactID: artifact.ID, Retrieval: "https://relay.example/artifact"}}
	if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: "agent-host:host-one"}); err != nil {
		t.Fatalf("agent host preflight: %v", err)
	}
	delete(distribution.agentHosts, "host-one")
	if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: "agent-host:host-one"}); err == nil {
		t.Fatal("removed agent host passed preflight")
	}
	if err := os.WriteFile(store.ArtifactPath(artifact), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: "direct-share"}); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("tampered artifact preflight: %v", err)
	}
	revoked, err := store.Build("deploy", "windows", "amd64", "revoked.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	revokedRecord := record
	revokedRecord.ArtifactID, revokedRecord.ArtifactSHA256 = revoked.ID, revoked.SHA256
	if _, err := executor.Preflight(context.Background(), revokedRecord, control.DeploymentStartRequest{Delivery: "direct-share"}); err == nil {
		t.Fatal("revoked artifact passed preflight")
	}
}

func TestWindowsDeploymentInstallPathAndShare(t *testing.T) {
	record := control.DeploymentRecord{ID: "deployment-one"}
	path, err := normalizeWindowsInstallPath(record, "", "worker.exe")
	if err != nil || path != `C:\ProgramData\Undertow\Deployments\deployment-one\worker.exe` {
		t.Fatalf("default path=%q err=%v", path, err)
	}
	share, err := windowsAdminSharePath("ws01", path)
	if err != nil || share != `\\ws01\C$\ProgramData\Undertow\Deployments\deployment-one\worker.exe` {
		t.Fatalf("share path=%q err=%v", share, err)
	}
	for _, invalid := range []string{`relative.exe`, `C:\temp\..\worker.exe`, `C:\temp\worker.dll`, `C:\bad?name\worker.exe`} {
		if _, err := normalizeWindowsInstallPath(record, invalid, "worker.exe"); err == nil {
			t.Fatalf("accepted invalid install path %q", invalid)
		}
	}
}

func TestWindowsDeploymentDirectShareMethods(t *testing.T) {
	methods := []struct {
		method, context, marker string
	}{
		{"winrm", "current-user", "Invoke-Command"},
		{"wmi", "current-user", "Win32_Process"},
		{"service-control", "local-system", "sc.exe"},
		{"scheduled-task", "local-system", "schtasks.exe"},
	}
	for _, item := range methods {
		t.Run(item.method, func(t *testing.T) {
			record := control.DeploymentRecord{ID: "0123456789abcdef", Target: "ws01", Method: item.method, Context: item.context}
			script, service, task, err := buildWindowsDeploymentScript(record, `C:\ProgramData\Undertow\agent.exe`, hostedArtifactInfo{}, true)
			if err != nil || !strings.Contains(string(script), item.marker) {
				t.Fatalf("script err=%v\n%s", err, script)
			}
			if strings.Contains(string(script), "FromBase64String('')") || strings.Contains(string(script), "https://") || strings.Contains(string(script), "smb-pipe://") {
				t.Fatalf("direct-share script contains retrieval material: %s", script)
			}
			if item.method == "service-control" && service == "" {
				t.Fatal("service name not recorded")
			}
			if item.method == "scheduled-task" && task == "" {
				t.Fatal("task name not recorded")
			}
		})
	}
}

func TestWindowsDeploymentHostedHelperDoesNotPersistSecretsInRecordShape(t *testing.T) {
	hosted := hostedArtifactInfo{Artifact: agentprofile.Artifact{Filename: "agent.exe", SHA256: strings.Repeat("a", 64)}, Retrieval: "https://relay.example:8443/" + strings.Repeat("b", 48), RetrievalPath: "/" + strings.Repeat("b", 48)}
	record := control.DeploymentRecord{ID: "deployment-one", Target: "ws01", Method: "winrm", Context: "current-user"}
	script, _, _, err := buildWindowsDeploymentScript(record, `C:\ProgramData\Undertow\agent.exe`, hosted, false)
	if err != nil || !strings.Contains(string(script), "Invoke-Command") {
		t.Fatalf("hosted WinRM script err=%v", err)
	}
	if strings.Contains(record.DeliveryID, hosted.RetrievalPath) || strings.Contains(record.InstallPath, hosted.RetrievalPath) {
		t.Fatal("deployment record contains retrieval capability")
	}
}

func TestGeneratedWindowsDeploymentScriptsParse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell parser is Windows-specific")
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	hosted := hostedArtifactInfo{Artifact: agentprofile.Artifact{Filename: "agent.exe", SHA256: strings.Repeat("a", 64)}, Retrieval: "https://relay.example:8443/" + strings.Repeat("b", 48), RetrievalPath: "/" + strings.Repeat("b", 48)}
	methods := []struct{ method, context string }{{"winrm", "current-user"}, {"wmi", "current-user"}, {"service-control", "local-system"}, {"scheduled-task", "local-system"}}
	for _, direct := range []bool{false, true} {
		for _, item := range methods {
			name := item.method + map[bool]string{false: "-hosted", true: "-direct"}[direct]
			t.Run(name, func(t *testing.T) {
				record := control.DeploymentRecord{ID: "0123456789abcdef", Target: "ws01", Method: item.method, Context: item.context}
				script, _, _, err := buildWindowsDeploymentScript(record, `C:\ProgramData\Undertow\agent.exe`, hosted, direct)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "deployment.ps1")
				if err := os.WriteFile(path, script, 0600); err != nil {
					t.Fatal(err)
				}
				command := `$path='` + strings.ReplaceAll(path, "'", "''") + `'; $errors=$null; [System.Management.Automation.Language.Parser]::ParseFile($path,[ref]$null,[ref]$errors) | Out-Null; if($errors.Count){$errors | ForEach-Object {$_.Message}; exit 1}`
				if output, err := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput(); err != nil {
					t.Fatalf("PowerShell parse failed: %v\n%s", err, output)
				}
			})
		}
	}
}
