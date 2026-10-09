//go:build windows && amd64

package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"undertow/internal/authcontext"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

// This UAT exercises the real control-plane background Job lifecycle. The
// companion pivot UAT covers direct exec, Live shell, ConPTY and a .NET worker.
func TestWindowsTokenContextUATImportedForeignProcessBackgroundJob(t *testing.T) {
	raw := os.Getenv("UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES")
	if raw == "" {
		t.Skip("set UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES in the Windows lab")
	}
	var identities []string
	if json.Unmarshal([]byte(raw), &identities) != nil || len(identities) < 2 {
		t.Fatal("UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES must name an alternate user and LocalSystem")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.ConfigureJobOutput(t.TempDir(), 1<<20, 2<<20); err != nil {
		t.Fatal(err)
	}
	server, agent := forwardAuditAgent(t, ctx, manager, "token-uat-agent", 991, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()

	discovered, err := pivot.ManageTokens(ctx, server, pivot.TokenRequest{Action: "discover"})
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range identities {
		var candidate authcontext.Metadata
		for _, item := range discovered.Candidates {
			if strings.EqualFold(item.Identity, identity) && strings.HasPrefix(item.Source, "process:") {
				candidate = item
				break
			}
		}
		if candidate.ID == "" {
			t.Fatalf("foreign process token for %q was not discoverable", identity)
		}
		imported, err := pivot.ManageTokens(ctx, server, pivot.TokenRequest{Action: "import", ID: candidate.ID})
		if err != nil || imported.Created == nil {
			t.Fatalf("import %s: %+v %v", identity, imported.Created, err)
		}
		id := imported.Created.ID
		job, err := manager.StartJob(pivot.WithTokenContext(ctx, id), 0, "token-uat-agent", []string{"whoami.exe", "/user"})
		if err != nil {
			t.Fatal(err)
		}
		finished := waitJob(t, manager, 0, job.ID, func(item JobInfo) bool {
			return item.State == "completed" || item.State == "failed"
		})
		if finished.State != "completed" || !strings.Contains(strings.ToLower(finished.Output), strings.ToLower(identity)) {
			t.Fatalf("background Job identity for %s: state=%s output=%q", identity, finished.State, finished.Output)
		}
		if _, err := pivot.ManageTokens(ctx, server, pivot.TokenRequest{Action: "remove", ID: id}); err != nil {
			t.Fatal(err)
		}
	}
}
