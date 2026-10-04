package control

import (
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/pivot"
)

func TestPrivilegeClassificationRequiresObservedEvidence(t *testing.T) {
	cases := []struct{ output, want string }{
		{"", ""},
		{"root group in text", ""},
		{"uid=1000(pickle) euid=1000(pickle)", "low"},
		{"uid=1000(user) euid=0(root)", "high"},
		{"Mandatory Label\\High Mandatory Level S-1-16-12288", "high"},
		{"Mandatory Label\\Medium Mandatory Level S-1-16-8192", "low"},
	}
	for _, c := range cases {
		if got := classifyPrivileges(c.output); got != c.want {
			t.Errorf("classify %q = %q, want %q", c.output, got, c.want)
		}
	}
}

func TestHostResultsRetainLatestExplicitRun(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := HostResult{AgentID: "agent-a", Operation: "privileges", SessionID: 5, At: time.Now().UTC(), Result: pivot.ExecResult{Stdout: "uid=1000", ExitCode: 0}}
	if err := store.SaveHostResult(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.SessionID = 6
	second.Result.Stdout = "uid=0"
	if err := store.SaveHostResult(second); err != nil {
		t.Fatal(err)
	}
	items, err := store.HostResults("agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SessionID != 6 || items[0].Result.Stdout != "uid=0" {
		t.Fatalf("latest result not retained: %+v", items)
	}
}
