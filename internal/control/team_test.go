package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"

	"undertow/internal/routing"
)

func TestTeamChatBindsSenderAndKeepsDirectMessagesPrivate(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BootstrapOperator("leader", "Team Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alice", "bobby", "carol"} {
		if err := store.CreateOperator(id, id, "another strong password", OperatorRole); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.operations = store
	m.clients[1] = &clientState{operator: mustTeamOperator(t, store, "alice")}
	m.clients[2] = &clientState{operator: mustTeamOperator(t, store, "bobby")}
	m.clients[3] = &clientState{operator: mustTeamOperator(t, store, "carol")}
	m.clients[4] = &clientState{operator: mustTeamOperator(t, store, "leader")}
	handler := m.handler("token")
	call := func(session uint64, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), actionContextKey{}, actionContext{ActionClaims: ActionClaims{OperatorID: "forged", DisplayName: "Forged", Source: "gui"}, ClientSessionID: session}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	posted := call(1, http.MethodPost, "/v1/team/messages", map[string]string{"recipient_id": "bobby", "body": "Can you review the route?"})
	if posted.Code != http.StatusCreated {
		t.Fatalf("post direct message: %d %s", posted.Code, posted.Body.String())
	}
	var message TeamMessage
	if err := json.Unmarshal(posted.Body.Bytes(), &message); err != nil || message.SenderID != "alice" || message.SenderName != "alice" {
		t.Fatalf("sender was not server-bound: %+v %v", message, err)
	}
	for _, test := range []struct {
		session uint64
		peer    string
		want    int
	}{{1, "bobby", 1}, {2, "alice", 1}, {3, "alice", 0}, {4, "alice", 0}} {
		response := call(test.session, http.MethodGet, "/v1/team/messages?peer="+test.peer, nil)
		var items []TeamMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &items) != nil || len(items) != test.want {
			t.Fatalf("session %d peer %s: status=%d messages=%s", test.session, test.peer, response.Code, response.Body.String())
		}
	}
	if response := call(1, http.MethodPost, "/v1/team/messages", map[string]string{"recipient_id": "carol", "body": "  "}); response.Code != http.StatusBadRequest {
		t.Fatalf("accepted empty message: %d", response.Code)
	}
	created := call(4, http.MethodPost, "/v1/team/tasks", map[string]string{"assignee_id": "bobby", "title": "Review route acceptance", "description": "Check the client path"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", created.Code, created.Body.String())
	}
	var task TeamTask
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil || task.CreatorID != "leader" || task.AssigneeID != "bobby" {
		t.Fatalf("created task: %+v %v", task, err)
	}
	path := "/v1/team/tasks/" + task.ID
	if response := call(3, http.MethodPut, path, map[string]string{"status": "done"}); response.Code != http.StatusForbidden {
		t.Fatalf("unrelated operator changed task: %d", response.Code)
	}
	if response := call(2, http.MethodPut, path, map[string]string{"status": "in_progress"}); response.Code != http.StatusOK {
		t.Fatalf("assignee could not start task: %d %s", response.Code, response.Body.String())
	}
	if response := call(4, http.MethodPut, path, map[string]string{"status": "done"}); response.Code != http.StatusOK {
		t.Fatalf("leader could not complete task: %d %s", response.Code, response.Body.String())
	}
	team := call(1, http.MethodGet, "/v1/team/messages", nil)
	var timeline []TeamMessage
	if team.Code != http.StatusOK || json.Unmarshal(team.Body.Bytes(), &timeline) != nil || len(timeline) != 3 {
		t.Fatalf("task activity not in team timeline: %d %s", team.Code, team.Body.String())
	}
	if timeline[0].Kind != "task_created" || timeline[2].Kind != "task_updated" {
		t.Fatalf("task activity kinds: %+v", timeline)
	}
	stored, err := store.TeamTasks()
	if err != nil || len(stored) != 1 || stored[0].Status != "done" {
		t.Fatalf("durable task state: %+v %v", stored, err)
	}
}

func mustTeamOperator(t *testing.T, store *OperationsStore, id string) OperatorAccount {
	t.Helper()
	account, err := store.Operator(id)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestTeamHistorySurvivesStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapOperator("leader", "Team Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperator("alice", "Alice", "another strong password", OperatorRole); err != nil {
		t.Fatal(err)
	}
	actor := mustTeamOperator(t, store, "leader")
	if _, err := store.PostTeamMessage(actor, "", "Shared handoff"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PostTeamMessage(actor, "alice", "Private handoff"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTeamTask(actor, "alice", "Review findings", "Before standup"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	shared, err := store.TeamMessages("alice", "", 0, 0)
	if err != nil || len(shared) != 2 || shared[0].Body != "Shared handoff" {
		t.Fatalf("shared history after restart: %+v %v", shared, err)
	}
	private, err := store.TeamMessages("alice", "leader", 0, 0)
	if err != nil || len(private) != 1 || private[0].Body != "Private handoff" {
		t.Fatalf("direct history after restart: %+v %v", private, err)
	}
	tasks, err := store.TeamTasks()
	if err != nil || len(tasks) != 1 || tasks[0].AssigneeID != "alice" {
		t.Fatalf("assignments after restart: %+v %v", tasks, err)
	}
}
