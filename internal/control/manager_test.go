package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

type routeDevice struct {
	mu     sync.Mutex
	routes map[string]bool
}

func (d *routeDevice) AddRoute(p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.routes[p] = true
	return nil
}
func (d *routeDevice) DelRoute(p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.routes, p)
	return nil
}
func (d *routeDevice) Has(p string) bool { d.mu.Lock(); defer d.mu.Unlock(); return d.routes[p] }

type idleTransport struct {
	done chan struct{}
	once sync.Once
}

func (t *idleTransport) Send(context.Context, []byte) error { return nil }
func (t *idleTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, context.Canceled
	}
}
func (t *idleTransport) Close() error { t.once.Do(func() { close(t.done) }); return nil }

func TestRouteActivationAndControlAuthentication(t *testing.T) {
	table := routing.New([]netip.Prefix{netip.MustParsePrefix("172.16.254.0/24")})
	device := &routeDevice{routes: make(map[string]bool)}
	manager := NewManager(table, device, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	prefix := netip.MustParsePrefix("10.20.0.0/16")
	if err := manager.AddRoute(prefix, "agent-a"); err != nil {
		t.Fatal(err)
	}
	if table.List()[0].Active {
		t.Fatal("route activated before agent connected")
	}
	var keys security.Keys
	for i := range keys.ClientToServer {
		keys.ClientToServer[i], keys.ServerToClient[i] = byte(i+1), byte(i+40)
	}
	s, err := session.New(101, keys, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	peer := &dns.Peer{Session: s, AgentID: "agent-a", Connected: time.Now(), VirtualIP: "172.16.254.2"}
	manager.Register(peer, streamMux)
	if peer.Snapshot().VirtualIP != "172.16.254.2" {
		t.Fatalf("virtual IP: %s", peer.Snapshot().VirtualIP)
	}
	if !table.List()[0].Active || !device.Has(prefix.String()) {
		t.Fatal("route did not activate")
	}
	if manager.Choose(netip.MustParseAddr("10.20.1.4")) != streamMux {
		t.Fatal("wrong agent selected")
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	response := httptest.NewRecorder()
	manager.handler("test-token").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status: %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	manager.handler("test-token").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status: %d", response.Code)
	}
	streamMux.Close()
	deadline := time.After(2 * time.Second)
	for table.List()[0].Active {
		select {
		case <-deadline:
			t.Fatal("route did not deactivate")
		case <-time.After(time.Millisecond):
		}
	}
	if device.Has(prefix.String()) {
		t.Fatal("owned OS route not removed")
	}
	if len(table.List()) != 1 {
		t.Fatal("configured route lost on disconnect")
	}
}
