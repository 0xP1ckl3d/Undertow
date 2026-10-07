//go:build linux || windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/pivot"
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

func TestWindowsDeploymentPreflightUsesAgentChannelAndRevalidatesArtifact(t *testing.T) {
	store, artifact := deploymentArtifactStore(t)
	executor := &windowsDeploymentExecutor{distribution: &agentDistribution{store: store}}
	record := control.DeploymentRecord{ID: "deployment-one", SourceAgentID: "source", ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Method: "winrm", Context: "current-user"}

	for _, delivery := range []string{"", "agent-channel"} {
		plan, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: delivery})
		if err != nil {
			t.Fatalf("delivery %q: %v", delivery, err)
		}
		if plan.DeliveryType != "agent-channel" || plan.DeliveryID != record.SourceAgentID {
			t.Fatalf("delivery %q plan: %+v", delivery, plan)
		}
	}
	for _, delivery := range []string{"direct-share", "server", "agent-host:host-one"} {
		if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Delivery: delivery}); err == nil {
			t.Fatalf("hosted delivery %q passed deployment preflight", delivery)
		}
	}

	if err := os.WriteFile(store.ArtifactPath(artifact), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{}); err == nil || !strings.Contains(err.Error(), "integrity") {
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
	if _, err := executor.Preflight(context.Background(), revokedRecord, control.DeploymentStartRequest{}); err == nil {
		t.Fatal("revoked artifact passed preflight")
	}
}

func TestWindowsDeploymentInstallPathAndShare(t *testing.T) {
	path, err := normalizeWindowsInstallPath("")
	if err != nil || !strings.HasPrefix(path, `C:\Windows\Temp\`) || !windowsInstallPath.MatchString(path) {
		t.Fatalf("default path=%q err=%v", path, err)
	}
	second, err := normalizeWindowsInstallPath("")
	if err != nil || second == path {
		t.Fatalf("second default path=%q first=%q err=%v", second, path, err)
	}
	if strings.Contains(path, "deployment-one") || strings.Contains(path, "worker") {
		t.Fatalf("default path exposes predictable deployment data: %q", path)
	}
	share, err := windowsAdminSharePath("ws01", path)
	if err != nil || !strings.HasPrefix(share, `\\ws01\ADMIN$\Temp\`) {
		t.Fatalf("share path=%q err=%v", share, err)
	}
	for _, invalid := range []string{`relative.exe`, `C:\temp\..\worker.exe`, `C:\temp\worker.dll`, `C:\bad?name\worker.exe`, `C:\bad:name\worker.exe`} {
		if _, err := normalizeWindowsInstallPath(invalid); err == nil {
			t.Fatalf("accepted invalid install path %q", invalid)
		}
	}
	override, err := windowsAdminSharePath("ws01", `D:\Apps\agent.exe`)
	if err != nil || override != `\\ws01\D$\Apps\agent.exe` {
		t.Fatalf("override share path=%q err=%v", override, err)
	}
}

func TestWindowsDeploymentPreflightAcceptsOptionalCredentialForms(t *testing.T) {
	store, artifact := deploymentArtifactStore(t)
	executor := &windowsDeploymentExecutor{distribution: &agentDistribution{store: store}}
	record := control.DeploymentRecord{ID: "deployment-one", SourceAgentID: "source", Target: "app01.example.com", ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Method: "winrm", Context: "current-user"}
	for _, account := range []string{"local-user", `LAB\operator`, "operator@example.com"} {
		plan, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Username: account, Password: "test password"})
		if err != nil || plan.Credential == nil || plan.Credential.Password != "test password" {
			t.Fatalf("account %q: plan=%+v err=%v", account, plan, err)
		}
	}
	for _, request := range []control.DeploymentStartRequest{{Username: "local-user"}, {Password: "test password"}, {Username: `bad\\account`, Password: "test password"}} {
		if _, err := executor.Preflight(context.Background(), record, request); err == nil {
			t.Fatalf("accepted invalid credential request: %+v", request)
		}
	}
}

func TestWindowsDeploymentPreflightAcceptsNTHashForSupportedMethods(t *testing.T) {
	store, artifact := deploymentArtifactStore(t)
	executor := &windowsDeploymentExecutor{distribution: &agentDistribution{store: store}}
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, item := range []struct{ method, context string }{{"wmi", "current-user"}, {"service-control", "local-system"}, {"scheduled-task", "local-system"}} {
		record := control.DeploymentRecord{ID: "deployment-one", SourceAgentID: "source", Target: "app01", ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Method: item.method, Context: item.context}
		plan, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Username: `LAB\operator`, NTHash: hash})
		if err != nil || plan.Credential == nil || plan.Credential.NTHash != hash {
			t.Fatalf("%s: plan=%+v err=%v", item.method, plan, err)
		}
	}
	for _, item := range []struct{ method, context string }{{"winrm", "current-user"}, {"scheduled-task", "current-user"}} {
		record := control.DeploymentRecord{ID: "deployment-one", SourceAgentID: "source", Target: "app01", ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Method: item.method, Context: item.context}
		if _, err := executor.Preflight(context.Background(), record, control.DeploymentStartRequest{Username: `LAB\operator`, NTHash: hash}); err == nil {
			t.Fatalf("accepted unsupported NT-hash method/context: %+v", item)
		}
	}
}

func TestWindowsDeploymentMethodsUseNativeToolsOnly(t *testing.T) {
	methods := []struct {
		method  string
		context string
	}{
		{"winrm", "current-user"},
		{"wmi", "current-user"},
		{"service-control", "local-system"},
		{"scheduled-task", "local-system"},
	}
	for _, item := range methods {
		t.Run(item.method, func(t *testing.T) {
			record := control.DeploymentRecord{ID: "0123456789abcdef", Target: "ws01", Method: item.method, Context: item.context}
			argv, service, task, err := buildWindowsDeploymentCommand(record, `C:\Windows\Temp\0123456789abcdef.exe`)
			if err != nil {
				t.Fatal(err)
			}
			command := strings.Join(argv, " ")
			if len(argv) != 7 || argv[0] != pivot.AgentExecutable || argv[1] != "_jump" || argv[2] != item.method {
				t.Fatalf("method worker command=%q", argv)
			}
			for _, forbidden := range []string{"undertow", "powershell", "invoke-command", ".ps1", "cmd.exe", "wmic.exe", "ping.exe", "https://", "smb-pipe://"} {
				if strings.Contains(strings.ToLower(command), forbidden) {
					t.Fatalf("native command contains %q: %q", forbidden, argv)
				}
			}
			if item.method == "service-control" && service != "S0123456789ab" {
				t.Fatalf("service name=%q", service)
			}
			if item.method == "scheduled-task" && task != "T0123456789ab" {
				t.Fatalf("task name=%q", task)
			}
			if item.method == "winrm" && task != "" {
				t.Fatalf("WinRM should not report a task name: %q", task)
			}
		})
	}
}

func TestWindowsDeploymentCurrentUserTaskUsesSourceIdentity(t *testing.T) {
	record := control.DeploymentRecord{ID: "deployment-one", Target: "ws01", Method: "scheduled-task", Context: "current-user"}
	argv, _, _, err := buildWindowsDeploymentCommand(record, `C:\Windows\Temp\agent.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 7 || argv[2] != "scheduled-task" || argv[5] != "current-user" {
		t.Fatalf("current-user task command=%q", argv)
	}
}

func TestWindowsDeploymentNativeCommandRejectsShellMetacharacters(t *testing.T) {
	record := control.DeploymentRecord{ID: "deployment-one", Target: "ws01", Method: "service-control", Context: "local-system"}
	if _, _, _, err := buildWindowsDeploymentCommand(record, `C:\Windows\Temp\bad&name.exe`); err == nil {
		t.Fatal("native command accepted shell metacharacters")
	}
}
