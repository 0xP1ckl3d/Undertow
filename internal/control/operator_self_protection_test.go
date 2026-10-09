package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorHTTPSelfProtection(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BootstrapOperator("leader", "Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperator("other", "Other Leader", "another strong password", TeamLeaderRole); err != nil {
		t.Fatal(err)
	}
	me, err := store.AuthenticateOperator("leader", "a unique strong password")
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{operations: store, clients: map[uint64]*clientState{1: {operator: me}}}
	muxer := http.NewServeMux()
	manager.operatorHTTPHandlers(muxer)
	request := func(method, id, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/operators/"+id, strings.NewReader(body))
		// The actor is bound to the authenticated session, not a supplied account ID.
		ctx := context.WithValue(r.Context(), actionContextKey{}, actionContext{
			ClientSessionID: 1,
			ActionClaims:    ActionClaims{OperatorID: "other"},
		})
		w := httptest.NewRecorder()
		muxer.ServeHTTP(w, r.WithContext(ctx))
		return w
	}
	for _, test := range []struct{ name, method, body string }{
		{"demote", http.MethodPut, `{"role":"operator"}`},
		{"disable", http.MethodPut, `{"disabled":true}`},
		{"combined update", http.MethodPut, `{"role":"operator","password":"a rotated strong password"}`},
		{"revoke", http.MethodDelete, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if w := request(test.method, "leader", test.body); w.Code != http.StatusForbidden {
				t.Fatalf("self %s: status=%d body=%s", test.name, w.Code, w.Body.String())
			}
			current, err := store.Operator("leader")
			if err != nil || current.Disabled || current.Revoked || current.Role != TeamLeaderRole || current.Version != me.Version {
				t.Fatalf("protected account changed: %+v, %v", current, err)
			}
		})
	}
	if w := request(http.MethodPut, "other", `{"role":"operator"}`); w.Code != http.StatusNoContent {
		t.Fatalf("manage another account: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, "leader", `{"password":"a rotated strong password"}`); w.Code != http.StatusNoContent {
		t.Fatalf("reset own password: status=%d body=%s", w.Code, w.Body.String())
	}
	// This HTTP-only fixture has no transport sessions for the deferred close.
	manager.mu.Lock()
	manager.clients = map[uint64]*clientState{}
	manager.mu.Unlock()
	if _, err := store.AuthenticateOperator("leader", "a rotated strong password"); err != nil {
		t.Fatalf("own password was not updated: %v", err)
	}
}
