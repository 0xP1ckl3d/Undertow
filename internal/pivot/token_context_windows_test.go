//go:build windows && amd64

package pivot

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"undertow/internal/authcontext"
)

func windowsTokenUATContext(t *testing.T) (context.Context, string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), tokenCapabilityKey{}, true)
	candidates, err := agentTokens.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range candidates {
		if m.Source == "agent process" {
			created, err := agentTokens.Import(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = agentTokens.Remove(created.ID) })
			return ctx, created.ID
		}
	}
	t.Fatal("agent process token was not discoverable")
	return nil, ""
}

func effectiveTokenSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

func TestWindowsTokenContextUATThreadRestorationAndConcurrentOperators(t *testing.T) {
	ctx, id := windowsTokenUATContext(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			original := effectiveTokenSID(t)
			selected, release, err := acquireTokenContext(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			restore, err := enterTokenThread(selected)
			if err != nil {
				t.Error(err)
				return
			}
			if effectiveTokenSID(t) != original {
				t.Error("own-process duplicate identity changed")
			}
			if err = restore(); err != nil {
				t.Error(err)
				return
			}
			if effectiveTokenSID(t) != original {
				t.Error("thread identity not restored")
			}
			var threadToken windows.Token
			if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &threadToken); err != windows.ERROR_NO_TOKEN {
				if err == nil {
					_ = threadToken.Close()
				}
				t.Error("operation left an impersonation token on its thread", err)
			}
		}()
	}
	wg.Wait()
}

func TestWindowsTokenContextUATWASMReportsSelectedIdentity(t *testing.T) {
	ctx, id := windowsTokenUATContext(t)
	selected, release, err := acquireTokenContext(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	metadata, _, err := operationTokenMetadata(selected)
	if err != nil {
		t.Fatal(err)
	}
	value, err := wasmHostOperation(selected, "system", wasmHostRequest{})
	if err != nil || value.(map[string]any)["user"] != metadata.Identity {
		t.Fatal("WASM host identity did not honour selected token", err)
	}
}

func TestWindowsTokenContextUATProtocolLifecycleAndCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	result, err := ManageTokens(ctx, server, TokenRequest{Action: "discover"})
	if err != nil {
		t.Fatal(err)
	}
	var candidate authcontext.Metadata
	for _, m := range result.Candidates {
		if m.Source == "agent process" {
			candidate = m
		}
	}
	if candidate.ID == "" {
		t.Fatal("agent candidate missing")
	}
	result, err = ManageTokens(ctx, server, TokenRequest{Action: "import", ID: candidate.ID})
	if err != nil || result.Created == nil {
		t.Fatal(result, err)
	}
	id := result.Created.ID
	t.Cleanup(func() { _ = agentTokens.Remove(id) })
	if result.Created.Identity == "" || result.Created.TokenType != "primary" || result.Created.IntegrityLevel == "" || result.Created.ElevationType == "" || result.Created.CreatedAt.IsZero() {
		t.Fatal("incomplete metadata", result.Created)
	}
	data, _ := json.Marshal(result)
	for _, field := range []string{"\"handle\"", "\"password\"", "\"sealed_logon\"", "\"credential_reference\""} {
		if strings.Contains(string(data), field) {
			t.Fatal("sensitive field in response")
		}
	}
	identity, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "whoami", TokenContextID: id})
	if err != nil || identity.Error != "" || strings.TrimSpace(identity.Stdout) != result.Created.Identity {
		t.Fatal(identity, err)
	}
	if _, err = ManageTokens(ctx, server, TokenRequest{Action: "remove", ID: id}); err != nil {
		t.Fatal(err)
	}
	stale, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "whoami", TokenContextID: id})
	if err != nil || !strings.Contains(stale.Error, "unavailable") {
		t.Fatal("stale context did not fail closed", stale, err)
	}
	original, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "whoami"})
	if err != nil || original.Error != "" || original.Stdout == "" {
		t.Fatal("process operation broken after removal", original, err)
	}
	deniedServer, deniedAgent := execTestMuxPair(ctx)
	defer deniedServer.Close()
	defer deniedAgent.Close()
	caps := DefaultCapabilities()
	caps.TokenContexts = false
	go ServeAgentWithCapabilities(ctx, deniedAgent, caps)
	if _, err = ManageTokens(ctx, deniedServer, TokenRequest{Action: "list"}); err == nil {
		t.Fatal("denied token capability accepted")
	}
	denied, err := ExecuteRequest(ctx, deniedServer, ExecRequest{Builtin: "whoami", TokenContextID: strings.Repeat("a", 32)})
	if err != nil || !strings.Contains(denied.Error, "disabled") {
		t.Fatal("denied context execution accepted", denied, err)
	}
}

func TestWindowsTokenContextUATChildProcess(t *testing.T) {
	ctx, id := windowsTokenUATContext(t)
	selected, release, err := acquireTokenContext(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	output, err := inventoryCommand(selected, "whoami.exe", "/user")
	if err != nil {
		t.Fatal(err)
	}
	token := operationToken(selected)
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, user.User.Sid.String()) {
		t.Fatal("child did not execute with selected token SID")
	}
}

func TestWindowsTokenContextUATSealedLogonFailureAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	response, err := ManageTokens(ctx, server, TokenRequest{Action: "creation-key"})
	if err != nil || response.CreationKey == nil {
		t.Fatal("creation key unavailable", err)
	}
	sealed, err := authcontext.SealLogon(*response.CreationKey, authcontext.LogonRequest{User: "undertow-uat-no-such-user-97bd2", Domain: ".", Password: "nonexistent-account-fixture", LogonType: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ManageTokens(ctx, server, TokenRequest{Action: "create", SealedLogon: &sealed})
	if err == nil || !strings.Contains(err.Error(), "Windows logon failed") {
		t.Fatal("invalid-account logon did not fail", err)
	}
	if strings.Contains(err.Error(), "nonexistent-account-fixture") {
		t.Fatal("password in logon error")
	}
	_, err = ManageTokens(ctx, server, TokenRequest{Action: "create", SealedLogon: &sealed})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatal("sealed logon replay accepted", err)
	}
}

// Optional lab coverage reads credentials from a protected file, never an
// environment variable or command line. No credential fields are printed.
func TestWindowsTokenContextUATAlternateLogon(t *testing.T) {
	path := os.Getenv("UNDERTOW_TOKEN_UAT_LOGON_FILE")
	if path == "" {
		t.Skip("set UNDERTOW_TOKEN_UAT_LOGON_FILE to a protected local JSON logon request in a Windows lab")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read UAT logon file")
	}
	defer clear(data)
	var logon authcontext.LogonRequest
	if json.Unmarshal(data, &logon) != nil {
		t.Fatal("invalid UAT logon fixture")
	}
	m, err := agentTokens.Create(context.Background(), logon)
	logon.Password = ""
	if err != nil {
		t.Fatal(err)
	}
	defer agentTokens.Remove(m.ID)
	ctx := context.WithValue(context.Background(), tokenCapabilityKey{}, true)
	selected, release, err := acquireTokenContext(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	restore, err := enterTokenThread(selected)
	if err != nil {
		t.Fatal(err)
	}
	u, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		_ = restore()
		t.Fatal(err)
	}
	selectedUser, err := operationToken(selected).GetTokenUser()
	if err != nil {
		_ = restore()
		t.Fatal(err)
	}
	if u.User.Sid.String() != selectedUser.User.Sid.String() {
		_ = restore()
		t.Fatal("alternate token not active")
	}
	if err = restore(); err != nil {
		t.Fatal(err)
	}
	output, err := inventoryCommand(selected, "whoami.exe", "/user")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, selectedUser.User.Sid.String()) {
		t.Fatal("alternate child did not use selected identity")
	}
}

// UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES is a JSON array containing the exact
// alternate-user and LocalSystem identity names expected in process-token
// discovery, for example ["LAB\\token-user","NT AUTHORITY\\SYSTEM"].
// This fixture contains no credential or handle material.
func TestWindowsTokenContextUATImportedForeignProcessExecution(t *testing.T) {
	raw := os.Getenv("UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES")
	if raw == "" {
		t.Skip("set UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES in the Windows lab")
	}
	var identities []string
	if json.Unmarshal([]byte(raw), &identities) != nil || len(identities) < 2 {
		t.Fatal("UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES must name an alternate user and LocalSystem")
	}
	assemblyPath := os.Getenv("UNDERTOW_TOKEN_UAT_IDENTITY_ASSEMBLY")
	if assemblyPath == "" {
		t.Fatal("UNDERTOW_TOKEN_UAT_IDENTITY_ASSEMBLY must point to the lab identity-reporting .NET Framework assembly")
	}
	assembly, err := os.ReadFile(assemblyPath)
	if err != nil {
		t.Fatal("read identity-reporting assembly:", err)
	}
	ctx := context.WithValue(context.Background(), tokenCapabilityKey{}, true)
	candidates, err := agentTokens.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range identities {
		var candidate authcontext.Metadata
		for _, item := range candidates {
			if strings.EqualFold(item.Identity, identity) && strings.HasPrefix(item.Source, "process:") {
				candidate = item
				break
			}
		}
		if candidate.ID == "" {
			t.Fatalf("foreign process token for %q was not discoverable", identity)
		}
		stored, err := agentTokens.Import(candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		selected, release, err := acquireTokenContext(ctx, stored.ID)
		if err != nil {
			_ = agentTokens.Remove(stored.ID)
			t.Fatal(err)
		}
		tokenUser, err := operationToken(selected).GetTokenUser()
		if err != nil {
			release()
			_ = agentTokens.Remove(stored.ID)
			t.Fatal(err)
		}
		expectedSID := tokenUser.User.Sid.String()
		output, err := inventoryCommand(selected, "whoami.exe", "/user")
		if err != nil || !strings.Contains(output, expectedSID) {
			release()
			_ = agentTokens.Remove(stored.ID)
			t.Fatalf("exec identity for %s: %v %q", identity, err, output)
		}
		for _, noPTY := range []bool{true, false} {
			process, terminal, _, err := startInteractiveProcess(selected, InteractiveRequest{Argv: []string{"cmd.exe", "/d", "/c", "whoami.exe /user"}, NoPTY: noPTY, Cols: 120, Rows: 30})
			if err != nil {
				release()
				_ = agentTokens.Remove(stored.ID)
				t.Fatalf("interactive identity for %s: %v", identity, err)
			}
			readDone := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(terminal); readDone <- data }()
			waitErr := process.Wait()
			_ = terminal.Close()
			data := <-readDone
			if waitErr != nil || !strings.Contains(string(data), expectedSID) {
				release()
				_ = agentTokens.Remove(stored.ID)
				t.Fatalf("interactive/background identity for %s: %v %q", identity, waitErr, data)
			}
		}
		var workerOutput strings.Builder
		code, workerErr := executeAssembly(selected, assembly, nil, func(kind byte, data []byte) error {
			if kind == InteractiveOutput || kind == InteractiveStderr {
				workerOutput.Write(data)
			}
			return nil
		})
		if workerErr != nil || code != 0 || !strings.Contains(workerOutput.String(), expectedSID) {
			release()
			_ = agentTokens.Remove(stored.ID)
			t.Fatalf("worker identity for %s: code=%d err=%v output=%q", identity, code, workerErr, workerOutput.String())
		}
		release()
		_ = agentTokens.Remove(stored.ID)
	}
}
