package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

type routeDevice struct {
	mu     sync.Mutex
	routes map[string]bool
}

func TestRejectedReconnectRetainsLastValidatedAgentInventory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	id := "known-agent"
	manager.offlineAgents[id] = AgentInfo{ID: id, Hostname: "WS01", ArtifactIdentity: ArtifactIdentity{ArtifactID: "expected-artifact"}, Offline: true}
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(9100, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: sess, AgentID: id, EnrollmentArtifactID: "expected-artifact", Connected: time.Now()}, serverMux)
	if got := manager.AgentCatalog(); len(got) != 1 || got[0].Hostname != "WS01" || got[0].Capabilities != nil {
		t.Fatalf("pending reconnect replaced last validated inventory: %+v", got)
	}
	manager.UpdateInventory(id, serverMux, []byte(`{"hostname":"WS01","artifact_id":"wrong-artifact"}`))
	manager.Unregister(id, serverMux)
	if got := manager.AgentCatalog(); len(got) != 1 || got[0].Hostname != "WS01" || got[0].ArtifactID != "expected-artifact" || !got[0].Offline {
		t.Fatalf("rejected reconnect erased last validated inventory: %+v", got)
	}
}

func TestTwoAgentsFromOnePayloadRemainConnected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.SetArtifactLookup(func(id string) (string, string, bool) {
		if id != "payload-one" {
			return "", "", false
		}
		return "office", "test", true
	})
	for i, id := range []string{"agent-one", "agent-two"} {
		a, b := make(chan []byte, 256), make(chan []byte, 256)
		serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
		agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
		defer serverMux.Close()
		defer agentMux.Close()
		var keys security.Keys
		sess, err := session.New(uint64(100+i), keys, false)
		if err != nil {
			t.Fatal(err)
		}
		manager.Register(&dns.Peer{Session: sess, AgentID: id, EnrollmentArtifactID: "payload-one", Connected: time.Now()}, serverMux)
		manager.UpdateInventory(id, serverMux, []byte(fmt.Sprintf(`{"hostname":"host-%d","artifact_id":"payload-one"}`, i)))
	}
	agents := manager.AgentList()
	if len(agents) != 2 || agents[0].ID == agents[1].ID || agents[0].VirtualIP == agents[1].VirtualIP {
		t.Fatalf("copies of one payload displaced each other: %+v", agents)
	}
	for _, agent := range agents {
		if agent.ArtifactID != "payload-one" || agent.Profile != "office" {
			t.Fatalf("artifact metadata lost: %+v", agent)
		}
	}
}

func TestPayloadRetrievalHostRoutesToDistributionHandler(t *testing.T) {
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.SetAgentDistributionHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/payload-retrieval-host" {
			t.Errorf("unexpected distribution path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		request := httptest.NewRequest(method, "/v1/payload-retrieval-host", nil)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		manager.handler("test-token").ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s retrieval host route: %d", method, response.Code)
		}
	}
}

func TestShutdownAcknowledgedAndLifecycleRecorded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(9001, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: sess, AgentID: "agent-a", Connected: time.Now()}, serverMux)
	manager.UpdateInventory("agent-a", serverMux, []byte(`{"artifact_id":"artifact-a","profile_id":"profile-a"}`))
	stopped := make(chan struct{})
	go pivot.ServeAgentWithLifecycle(ctx, agentMux, pivot.DefaultCapabilities(), func() { close(stopped) })
	if err := manager.ShutdownAgent(ctx, "agent-a"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("agent did not stop after acknowledged shutdown")
	}
	events := manager.LifecycleEvents("agent-a")
	if len(events) < 3 || events[0].Kind != "shutdown_acknowledged" || events[1].Kind != "shutdown_requested" || events[2].Kind != "connected" {
		t.Fatalf("events=%+v", events)
	}
}

func TestLifecycleHistoryIsBounded(t *testing.T) {
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.mu.Lock()
	for i := 0; i < 1200; i++ {
		manager.recordLifecycleLocked(LifecycleEvent{AgentID: "agent-a", Kind: "connected"})
	}
	count := len(manager.lifecycleEvents)
	manager.mu.Unlock()
	if count != 1024 || len(manager.LifecycleEvents("agent-a")) != 32 {
		t.Fatalf("history=%d recent=%d", count, len(manager.LifecycleEvents("agent-a")))
	}
}

func TestVPNClientAppearsInStatusAndIsRemoved(t *testing.T) {
	table := routing.New(nil)
	manager := NewManager(table, nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.SetServerInfo(ServerInfo{Transport: "quic", Network: "udp", Listen: "0.0.0.0:443", TLSMode: "self-signed", Fingerprint: "server-pin"})
	var keys security.Keys
	s, err := session.New(702, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	peer := &dns.Peer{Session: s, AgentID: "client-id", Remote: "203.0.113.7:50000", Connected: time.Now(), LastSeen: time.Now()}
	manager.RegisterClient(peer, streamMux, true, "vpn-host")
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	manager.handler("test-token").ServeHTTP(response, request)
	var status struct {
		Server  ServerInfo   `json:"server"`
		Agents  []AgentInfo  `json:"agents"`
		Clients []ClientInfo `json:"clients"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Server.Transport != "quic" || status.Server.Listen != "0.0.0.0:443" || status.Server.TLSMode != "self-signed" || len(status.Agents) != 0 || len(status.Clients) != 1 || status.Clients[0].SessionID != 702 || !status.Clients[0].Internal || status.Clients[0].Hostname != "vpn-host" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if err := manager.SetClientInternal(702, false); err != nil || manager.ClientInternal(702) {
		t.Fatalf("interactive internal mode change failed: %v", err)
	}
	manager.UnregisterClient(702, streamMux)
	if got := manager.ClientList(); len(got) != 0 {
		t.Fatalf("disconnected client remains in status: %+v", got)
	}
	streamMux.Close()
}

func TestAcceptedRoutesStayLocalToVPNClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	agentMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer agentMux.Close()
	var keys security.Keys
	agentSession, _ := session.New(801, keys, false)
	manager.Register(&dns.Peer{Session: agentSession, AgentID: "agent-a", Connected: time.Now()}, agentMux)
	manager.UpdateInventory("agent-a", agentMux, []byte(`{"advertised_routes":["192.168.0.0/22"],"capabilities":{"supported":["pivot","exec","upload","download"],"allowed":["pivot","download"]}}`))
	if agents := manager.AgentList(); len(agents) != 1 || agents[0].Capabilities == nil || len(agents[0].Capabilities.Allowed) != 2 {
		t.Fatalf("agent capabilities were not reflected in status: %+v", agents)
	}
	clientMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer clientMux.Close()
	clientSession, _ := session.New(802, keys, false)
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client-a", Connected: time.Now()}, clientMux, false, "")
	otherMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer otherMux.Close()
	otherSession, _ := session.New(803, keys, false)
	manager.RegisterClient(&dns.Peer{Session: otherSession, AgentID: "client-b", Connected: time.Now()}, otherMux, false, "")
	advertised := netip.MustParsePrefix("192.168.0.0/22")
	manual := netip.MustParsePrefix("10.10.0.0/16")
	if err := manager.SetClientRoute(802, manual, "agent-a", false); err == nil {
		t.Fatal("accepted a nonadvertised route without manual mode")
	}
	if err := manager.SetClientRoute(802, advertised, "agent-a", false); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetClientRoute(802, manual, "agent-a", true); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"192.168.1.25", "10.10.4.9"} {
		if got, ok := manager.ResolveClientEgress(802, netip.MustParseAddr(target)); !ok || got != agentMux {
			t.Fatalf("client route %s did not use agent", target)
		}
		if got, ok := manager.ResolveClientEgress(803, netip.MustParseAddr(target)); ok || got != nil {
			t.Fatalf("route %s leaked to another client", target)
		}
	}
	if len(manager.routes.List()) != 0 {
		t.Fatal("client acceptance changed server global routes")
	}
	if err := manager.DeleteClientRoute(802, manual); err != nil {
		t.Fatal(err)
	}
	if got, ok := manager.ResolveClientEgress(802, netip.MustParseAddr("10.10.4.9")); ok || got != nil {
		t.Fatal("removed client route still resolves")
	}
}

func TestSleepingAgentAcceptsRouteWithoutPretendingItCanCarryTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	clientMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer clientMux.Close()
	var keys security.Keys
	clientSession, _ := session.New(806, keys, false)
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client-a", Connected: time.Now()}, clientMux, false, "")
	manager.mu.Lock()
	manager.offlineAgents["sleeping-agent"] = AgentInfo{ID: "sleeping-agent", ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute), AdvertisedRoutes: []string{"10.44.0.0/16"}}
	manager.mu.Unlock()
	prefix := netip.MustParsePrefix("10.44.0.0/16")
	if err := manager.SetClientRoute(806, prefix, "sleeping-agent", false); err != nil {
		t.Fatal(err)
	}
	if got, accepted := manager.ResolveClientEgress(806, netip.MustParseAddr("10.44.1.1")); got != nil || !accepted {
		t.Fatalf("sleeping route availability was misreported: %v %v", got, accepted)
	}
	manager.mu.Lock()
	lost := manager.offlineAgents["sleeping-agent"]
	lost.ConnectionState = "disconnected"
	manager.offlineAgents["sleeping-agent"] = lost
	manager.mu.Unlock()
	if err := manager.SetClientRoute(806, netip.MustParsePrefix("10.45.0.0/16"), "sleeping-agent", true); err == nil {
		t.Fatal("accepted new route through genuinely lost agent")
	}
}

func TestClientRouteOwnershipRequiresOldAgentToDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	var keys security.Keys
	for i, id := range []string{"old-agent", "new-agent"} {
		streamMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
		defer streamMux.Close()
		sess, err := session.New(uint64(900+i), keys, false)
		if err != nil {
			t.Fatal(err)
		}
		manager.Register(&dns.Peer{Session: sess, AgentID: id, Connected: time.Now()}, streamMux)
		manager.UpdateInventory(id, streamMux, []byte(`{"capabilities":{"allowed":["pivot"]}}`))
	}
	clientMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer clientMux.Close()
	clientSession, err := session.New(902, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client", Connected: time.Now()}, clientMux, false, "")
	prefix := netip.MustParsePrefix("10.10.10.0/24")
	if err := manager.SetClientRoute(902, prefix, "old-agent", true); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetClientRoute(902, prefix, "new-agent", true); err == nil || !strings.Contains(err.Error(), "owned by connected agent old-agent") {
		t.Fatalf("connected owner was replaced: %v", err)
	}
	if got := manager.ClientList()[0].AcceptedRoutes; len(got) != 1 || got[0].AgentID != "old-agent" {
		t.Fatalf("owner changed after rejection: %+v", got)
	}
	manager.mu.RLock()
	oldMux := manager.agents["old-agent"].mux
	manager.mu.RUnlock()
	manager.Unregister("old-agent", oldMux)
	if err := manager.SetClientRoute(902, prefix, "new-agent", true); err != nil {
		t.Fatal(err)
	}
	if got := manager.ClientList()[0].AcceptedRoutes; len(got) != 1 || got[0].AgentID != "new-agent" {
		t.Fatalf("offline owner was not replaced: %+v", got)
	}
}

func TestStructuredDiscoveredRoutesAreCandidateOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	agentMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer agentMux.Close()
	var keys security.Keys
	agentSession, _ := session.New(811, keys, false)
	manager.Register(&dns.Peer{Session: agentSession, AgentID: "agent-a", Connected: time.Now()}, agentMux)
	manager.UpdateInventory("agent-a", agentMux, []byte(`{"hostname":"pivot","capabilities":{"allowed":["pivot"]}}`))
	manager.UpdateInventory("agent-a", agentMux, []byte(`{"route_update":true,"reset":true,"routes":[{"prefix":"10.40.0.0/16","gateway":"192.168.50.1","interface":"eth0","type":"unicast","source":"static","direct":false},{"prefix":"172.16.254.0/24","interface":"eth0","direct":true}],"default_route":{"prefix":"0.0.0.0/0","gateway":"192.168.50.1","interface":"eth0"}}`))
	agents := manager.AgentList()
	if len(agents) != 1 || len(agents[0].Routes) != 1 || agents[0].Routes[0].Prefix != "10.40.0.0/16" || agents[0].Routes[0].Gateway != "192.168.50.1" || agents[0].DefaultRoute == nil {
		t.Fatalf("structured inventory=%+v", agents)
	}
	if len(manager.routes.List()) != 0 {
		t.Fatal("discovered routes were installed globally")
	}
	clientMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer clientMux.Close()
	clientSession, _ := session.New(812, keys, false)
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client-a", Connected: time.Now()}, clientMux, false, "")
	if err := manager.SetClientRoute(812, netip.MustParsePrefix("10.40.0.0/16"), "agent-a", false); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetClientRoute(812, netip.MustParsePrefix("0.0.0.0/0"), "agent-a", false); err == nil {
		t.Fatal("default route accepted as discovered candidate")
	}
	if got, configured := manager.ResolveClientEgress(812, netip.MustParseAddr("10.40.1.3")); !configured || got != agentMux {
		t.Fatal("accepted candidate did not resolve")
	}
}

func TestDeniedPivotDeactivatesConfiguredRoute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	device := &routeDevice{routes: make(map[string]bool)}
	manager := NewManager(routing.New(nil), device, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	prefix := netip.MustParsePrefix("10.10.0.0/16")
	if err := manager.AddRoute(prefix, "agent-a"); err != nil {
		t.Fatal(err)
	}
	streamMux := mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	defer streamMux.Close()
	var keys security.Keys
	s, err := session.New(811, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: s, AgentID: "agent-a", Connected: time.Now()}, streamMux)
	if device.Has(prefix.String()) || manager.routes.List()[0].Active {
		t.Fatal("configured route activated before agent inventory arrived")
	}
	manager.UpdateInventory("agent-a", streamMux, []byte(`{"capabilities":{"supported":["pivot","exec","upload","download"],"allowed":["pivot","exec","upload","download"]}}`))
	if !device.Has(prefix.String()) || !manager.routes.List()[0].Active {
		t.Fatal("configured route did not activate after pivot was allowed")
	}
	manager.UpdateInventory("agent-a", streamMux, []byte(`{"capabilities":{"supported":["pivot","exec","upload","download"],"allowed":["exec","upload","download"]}}`))
	if device.Has(prefix.String()) || manager.routes.List()[0].Active {
		t.Fatal("route remained active after agent denied pivot")
	}
	if err := manager.AddRoute(netip.MustParsePrefix("10.20.0.0/16"), "agent-a"); err != nil {
		t.Fatal(err)
	}
	if manager.routes.List()[1].Active {
		t.Fatal("new route activated through agent with pivot disabled")
	}
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
	if table.List()[0].Active || device.Has(prefix.String()) {
		t.Fatal("route activated before agent inventory arrived")
	}
	manager.UpdateInventory("agent-a", streamMux, []byte(`{"hostname":"test-agent"}`))
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
