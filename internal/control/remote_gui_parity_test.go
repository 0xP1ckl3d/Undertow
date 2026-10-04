package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteGUIManagementAllowlistIsScoped(t *testing.T) {
	cases := []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/v1/agents/agent-a", true},
		{http.MethodGet, "/v1/agents/agent-a/other", false},
		{http.MethodGet, "/v1/worker-logs?after=12", true},
		{http.MethodGet, "/v1/worker-logs?after=nope", false},
		{http.MethodDelete, "/v1/transports/quic?force=true", true},
		{http.MethodDelete, "/v1/transports/quic?force=false", false},
		{http.MethodPost, "/v1/clients/705/forwards", true},
		{http.MethodPost, "/v1/clients/706/forwards", false},
		{http.MethodPost, "/v1/clients/705/vpn", true},
		{http.MethodPost, "/v1/clients/706/vpn", false},
		{http.MethodGet, "/v1/transfers", true},
		{http.MethodPost, "/v1/transfers", true},
		{http.MethodPut, "/v1/transfers/record-a", true},
		{http.MethodPut, "/v1/transfers/record-a/other", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := clientRequestAllowed(r, 705); got != tc.allowed {
			t.Errorf("%s %s: got %t, want %t", tc.method, tc.path, got, tc.allowed)
		}
	}
}
