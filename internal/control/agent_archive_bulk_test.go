package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"undertow/internal/routing"
)

func bulkArchiveManager(t *testing.T) (*Manager, *OperationsStore) {
	t.Helper()
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"archive-a", "archive-b"} {
		if err := store.SaveAgentSnapshot(AgentInfo{ID: id, Hostname: id}); err != nil {
			t.Fatal(err)
		}
		if err := store.SetAgentArchived(id, true); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	return m, store
}

func TestBulkArchiveRestoreValidatesWholeSelection(t *testing.T) {
	m, store := bulkArchiveManager(t)
	if err := m.ChangeArchivedAgents([]string{"archive-a", "missing"}, "restore"); err == nil {
		t.Fatal("mixed selection was accepted")
	}
	if !m.archivedAgents["archive-a"] {
		t.Fatal("partial restore changed memory")
	}
	if archived, err := store.LoadArchivedAgents(); err != nil || !archived["archive-a"] {
		t.Fatalf("partial restore changed database: %+v %v", archived, err)
	}
	if err := m.ChangeArchivedAgents([]string{"archive-a", "archive-b"}, "restore"); err != nil {
		t.Fatal(err)
	}
	if archived, err := store.LoadArchivedAgents(); err != nil || len(archived) != 0 {
		t.Fatalf("restore not durable: %+v %v", archived, err)
	}
	if catalog := m.AgentCatalog(); len(catalog) != 2 || catalog[0].Archived || catalog[1].Archived {
		t.Fatalf("restored catalog: %+v", catalog)
	}
}

func TestArchivingAgentImmediatelyRemovesOwnedRoutes(t *testing.T) {
	m, store := bulkArchiveManager(t)
	if err := store.SaveAgentSnapshot(AgentInfo{ID: "lost-agent", Hostname: "WS01"}); err != nil {
		t.Fatal(err)
	}
	m.offlineAgents["lost-agent"] = AgentInfo{ID: "lost-agent", Hostname: "WS01"}
	prefix := netip.MustParsePrefix("10.10.0.0/16")
	if err := m.AddRoute(prefix, "lost-agent"); err != nil {
		t.Fatal(err)
	}
	m.clients[1] = &clientState{accepted: map[netip.Prefix]AcceptedRoute{prefix: {Prefix: prefix.String(), AgentID: "lost-agent"}}}
	if err := m.SetAgentArchived("lost-agent", true); err != nil {
		t.Fatal(err)
	}
	if len(m.routes.List()) != 0 || len(m.clients[1].accepted) != 0 {
		t.Fatalf("archived routes survived: global=%+v accepted=%+v", m.routes.List(), m.clients[1].accepted)
	}
	if archived, err := store.LoadArchivedAgents(); err != nil || !archived["lost-agent"] {
		t.Fatalf("archive did not persist: %+v %v", archived, err)
	}
}

func TestPermanentArchiveDeletionRequiresTeamLeader(t *testing.T) {
	m, store := bulkArchiveManager(t)
	if err := store.BootstrapOperator("leader", "Team Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperator("alice", "Alice", "another strong password", OperatorRole); err != nil {
		t.Fatal(err)
	}
	m.clients[1] = &clientState{operator: mustTeamOperator(t, store, "alice")}
	m.clients[2] = &clientState{operator: mustTeamOperator(t, store, "leader")}
	call := func(session uint64) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/archive/bulk", bytes.NewBufferString(`{"action":"delete","ids":["archive-a"]}`))
		request.Header.Set("Authorization", "Bearer token")
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(context.WithValue(request.Context(), actionContextKey{}, actionContext{ClientSessionID: session}))
		response := httptest.NewRecorder()
		m.handler("token").ServeHTTP(response, request)
		return response
	}
	if response := call(1); response.Code != http.StatusForbidden {
		t.Fatalf("operator deleted agent: %d %s", response.Code, response.Body.String())
	}
	if countBulkTestRows(t, store, "agent_snapshots", "id", "archive-a") != 1 {
		t.Fatal("forbidden deletion changed records")
	}
	if response := call(2); response.Code != http.StatusOK {
		t.Fatalf("Team Leader deletion failed: %d %s", response.Code, response.Body.String())
	}
}

func TestBulkArchiveDeletePurgesOnlySelectedAgentRecords(t *testing.T) {
	m, store := bulkArchiveManager(t)
	root := filepath.Join(t.TempDir(), "job-output")
	if err := m.ConfigureJobOutput(root, 1024, 4096); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(root, "archive-a")
	if err := os.MkdirAll(jobDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "job-a.out"), []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}
	shotID := "0123456789abcdef0123456789abcdef"
	if err := os.MkdirAll(store.screenshotsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.screenshotPath(shotID), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	jobJSON, _ := json.Marshal(JobInfo{ID: "job-a", AgentID: "archive-a"})
	otherJobJSON, _ := json.Marshal(JobInfo{ID: "job-b", AgentID: "archive-b"})
	for _, row := range []struct {
		id   string
		data []byte
	}{{"job-a", jobJSON}, {"job-b", otherJobJSON}} {
		if _, err := store.db.Exec(`INSERT INTO job_records(id,info_json,owner_key,output_path) VALUES(?,?,?,?)`, row.id, row.data, "owner", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO agent_nicknames VALUES('archive-a','Old name')`,
		`INSERT INTO agent_sleep VALUES('archive-a',15,20)`,
		`INSERT INTO relay_listeners VALUES('archive-a','10.0.0.1:8443')`,
		`INSERT INTO queued_job_requests VALUES('job-a','{}')`,
		`INSERT INTO screenshots VALUES('` + shotID + `','archive-a',0,'2026-01-01',5,'hash',1,1,'client','1','operator','Operator')`,
		`INSERT INTO deployments(id,record_json,state,source_agent_id,artifact_id,target,result_agent_id,created_at) VALUES('deploy-a','{}','complete','archive-a','artifact','host','','2026-01-01')`,
		`INSERT INTO transfers(id,record_json,client_session_id,started) VALUES('transfer-a','{"agent_id":"archive-a"}','1','2026-01-01')`,
		`INSERT INTO transfers(id,record_json,client_session_id,started) VALUES('transfer-b','{"agent_id":"archive-b"}','1','2026-01-01')`,
	} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.AddRoute(netip.MustParsePrefix("10.10.0.0/16"), "archive-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.ChangeArchivedAgents([]string{"archive-a", "archive-b", "missing"}, "delete"); err == nil {
		t.Fatal("mixed delete selection was accepted")
	}
	if countBulkTestRows(t, store, "agent_snapshots", "id", "archive-a") != 1 {
		t.Fatal("invalid selection partially deleted a record")
	}
	if err := m.ChangeArchivedAgents([]string{"archive-a"}, "delete"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ table, column, id string }{
		{"agent_snapshots", "id", "archive-a"}, {"agent_archives", "id", "archive-a"},
		{"agent_nicknames", "id", "archive-a"}, {"agent_sleep", "id", "archive-a"},
		{"relay_listeners", "agent_id", "archive-a"}, {"job_records", "id", "job-a"},
		{"queued_job_requests", "id", "job-a"}, {"screenshots", "id", shotID},
		{"deployments", "id", "deploy-a"}, {"transfers", "id", "transfer-a"},
	} {
		if got := countBulkTestRows(t, store, row.table, row.column, row.id); got != 0 {
			t.Fatalf("%s retained %s: %d rows", row.table, row.id, got)
		}
	}
	if got := countBulkTestRows(t, store, "agent_snapshots", "id", "archive-b"); got != 1 {
		t.Fatalf("unselected agent was deleted: %d", got)
	}
	if got := countBulkTestRows(t, store, "job_records", "id", "job-b"); got != 1 {
		t.Fatalf("unselected job was deleted: %d", got)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job output still exists: %v", err)
	}
	if _, err := os.Stat(store.screenshotPath(shotID)); !os.IsNotExist(err) {
		t.Fatalf("screenshot still exists: %v", err)
	}
	if len(m.routes.List()) != 0 || len(m.AgentCatalog()) != 1 {
		t.Fatalf("deleted agent still appears: routes=%+v agents=%+v", m.routes.List(), m.AgentCatalog())
	}
}

func countBulkTestRows(t *testing.T, store *OperationsStore, table, column, id string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+"=?", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
