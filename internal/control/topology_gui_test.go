package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func TestTopologyShowsEveryCarrierAndConfiguredServerHost(t *testing.T) {
	topology := BuildTopology(nil, nil, nil, nil, nil, ServerInfo{PublicHost: "gateway.example.test", Listeners: []ListenerInfo{{Transport: "quic", Listen: "0.0.0.0:443", Sessions: 2}}})
	if topology.Version < 3 || len(topology.Nodes) == 0 {
		t.Fatalf("topology=%+v", topology)
	}
	server := topology.Nodes[0]
	if server.PublicHost != "gateway.example.test" || len(server.Carriers) != 3 {
		t.Fatalf("server=%+v", server)
	}
	if server.Carriers[0].Transport != "dns" || server.Carriers[0].Active || server.Carriers[1].Transport != "quic" || !server.Carriers[1].Active || server.Carriers[1].Sessions != 2 || server.Carriers[2].Transport != "websocket" || server.Carriers[2].Active {
		t.Fatalf("carriers=%+v", server.Carriers)
	}
}

func TestLegacyAgentPrivilegeRestoredFromExplicitResult(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveAgentSnapshot(AgentInfo{ID: "legacy-agent", Hostname: "WS01"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHostResult(HostResult{AgentID: "legacy-agent", Operation: "privileges", Result: pivot.ExecResult{Stdout: "Mandatory Label\\High Mandatory Level S-1-16-12288"}}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	catalog := manager.AgentCatalog()
	if len(catalog) != 1 || catalog[0].Privilege != "high" {
		t.Fatalf("restored privilege: %+v", catalog)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(988, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: sess, AgentID: "legacy-agent", Connected: time.Now()}, serverMux)
	manager.UpdateInventory("legacy-agent", serverMux, []byte(`{"hostname":"WS01","os":"windows"}`))
	live := manager.AgentList()
	if len(live) != 1 || live[0].Privilege != "high" {
		t.Fatalf("legacy reconnect privilege: %+v", live)
	}
	manager.ShutdownAgentSessions()
	saved, err := store.LoadAgentSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Online || saved[0].Privilege != "high" {
		t.Fatalf("shutdown snapshot: %+v", saved)
	}
}

func TestArchivedAgentRestoresOnCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgentSnapshot(AgentInfo{ID: "lost-agent", Hostname: "WS01"}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/v1/agents/lost-agent/archive", bytes.NewBufferString(`{"archived":true}`))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	manager.handler("test-token").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("archive API: %d %s", response.Code, response.Body.String())
	}
	if catalog := manager.AgentCatalog(); len(catalog) != 1 || !catalog[0].Archived {
		t.Fatalf("archived catalog: %+v", catalog)
	}
	audit, err := store.AuditHistory(5)
	if err != nil || len(audit) == 0 || audit[0].Target != "/v1/agents/lost-agent/archive" || audit[0].Status != http.StatusOK {
		t.Fatalf("archive audit: %v %+v", err, audit)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restarted := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := restarted.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	if catalog := restarted.AgentCatalog(); len(catalog) != 1 || !catalog[0].Archived {
		t.Fatalf("archive did not survive restart: %+v", catalog)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(989, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	restarted.Register(&dns.Peer{Session: sess, AgentID: "lost-agent", Connected: time.Now()}, serverMux)
	if catalog := restarted.AgentCatalog(); len(catalog) != 1 || catalog[0].Archived || !catalog[0].Online {
		t.Fatalf("callback did not restore agent: %+v", catalog)
	}
	archived, err := store.LoadArchivedAgents()
	if err != nil || archived["lost-agent"] {
		t.Fatalf("callback did not clear durable archive: %v %+v", err, archived)
	}
	if err := restarted.SetAgentArchived("lost-agent", true); err == nil {
		t.Fatal("connected agent was archived")
	}
	restarted.ShutdownAgentSessions()
	if catalog := restarted.AgentCatalog(); len(catalog) != 1 || catalog[0].Archived || catalog[0].Online {
		t.Fatalf("agent re-archived after disconnect: %+v", catalog)
	}
}

func TestArchivedAgentSurvivesSnapshotRetention(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveAgentSnapshot(AgentInfo{ID: "archived-first"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentArchived("archived-first", true); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("agent-%04d", i)
		data, _ := json.Marshal(AgentInfo{ID: id})
		if _, err := tx.Exec(`INSERT INTO agent_snapshots(id,saved_at,info_json) VALUES(?,?,?)`, id, fmt.Sprintf("2099-01-01T%02d:%02d:%02dZ", i/3600, i/60%60, i%60), data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgentSnapshot(AgentInfo{ID: "retention-trigger"}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := store.LoadAgentSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == "archived-first" {
			return
		}
	}
	t.Fatal("archived agent was removed by snapshot retention")
}

func TestConfiguredRelayRestoresOnParentCheckin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ops.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bind := probe.Addr().String()
	probe.Close()
	if err := store.SetRelayListener("parent", bind, true); err != nil {
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
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(987, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: sess, AgentID: "parent", Connected: time.Now()}, serverMux)
	go pivot.ServeAgentWithCapabilities(ctx, agentMux, pivot.DefaultCapabilities())
	manager.UpdateInventory("parent", serverMux, []byte(`{"hostname":"WS01","capabilities":{"allowed":["relay"]}}`))
	deadline := time.Now().Add(5 * time.Second)
	for len(manager.RelayList("parent")) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	relays := manager.RelayList("parent")
	if len(relays) != 1 {
		t.Fatalf("relay did not restore: %+v", relays)
	}
	if err := manager.StopRelay("parent", relays[0].Bind); err != nil {
		t.Fatal(err)
	}
	configured, err := store.LoadRelayListeners()
	if err != nil || len(configured) != 0 {
		t.Fatalf("stopped relay still desired: %+v %v", configured, err)
	}
	started, err := manager.StartRelay(ctx, "parent", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	configured, err = store.LoadRelayListeners()
	if err != nil || len(configured) != 1 || configured[0].Bind != started.Bind {
		t.Fatalf("started relay not persisted: %+v %v", configured, err)
	}
	if err := manager.StopRelay("parent", started.Bind); err != nil {
		t.Fatal(err)
	}
	manager.Unregister("parent", serverMux)
}

func TestTopologyCarriesEachClientsRoutingModes(t *testing.T) {
	clients := []ClientInfo{{ID: "vpn-client", SessionID: 41, VPN: true, Internal: false}, {ID: "internal-client", SessionID: 42, VPN: false, Internal: true}}
	topology := BuildTopology(nil, clients, nil, nil, nil)
	seen := map[string]TopologyEdge{}
	for _, edge := range topology.Edges {
		if edge.Kind == "carrier" {
			seen[edge.ClientID] = edge
		}
	}
	if !seen["vpn-client"].VPN || seen["vpn-client"].Internal || seen["internal-client"].VPN || !seen["internal-client"].Internal {
		t.Fatalf("client modes in topology: %+v", seen)
	}
	if !VPNEnabled([]byte(`{"mode":"vpn","vpn":true}`)) || VPNEnabled([]byte(`{"mode":"vpn"}`)) {
		t.Fatal("VPN handshake mode was not decoded")
	}
}

func TestPublicHostPersistsAndRejectsInvalidNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPublicHost("gateway.example.test"); err != nil {
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
	host, err := store.PublicHost()
	if err != nil || host != "gateway.example.test" {
		t.Fatalf("host=%q err=%v", host, err)
	}
	for _, invalid := range []string{"https://example.test", "example.test:443", "-bad.test", "a..b", "127.0.0.1:443"} {
		if validServerPublicHost(invalid) {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestObservedPublicIPDoesNotGuessPrivateAddress(t *testing.T) {
	if got := observedPublicIP("192.168.50.1:4141"); got != "" {
		t.Fatalf("private IP=%q", got)
	}
	if got := observedPublicIP("8.8.8.8:4141"); got != "8.8.8.8" {
		t.Fatalf("global IP=%q", got)
	}
}

func TestDisconnectedAgentRemainsInServerCatalogAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ops.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(888, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: sess, AgentID: "agent-record", Connected: time.Now()}, serverMux)
	manager.UpdateInventory("agent-record", serverMux, []byte(`{"hostname":"WS01","os":"windows","privilege":"high","advertised_routes":["10.44.0.0/16"]}`))
	if err := manager.SetAgentNickname("agent-record", "File server relay"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAgentNickname("agent-record", "bad\nname"); err == nil {
		t.Fatal("accepted multiline nickname")
	}
	manager.Unregister("agent-record", serverMux)
	if len(manager.AgentList()) != 0 {
		t.Fatal("offline agent remained operational")
	}
	catalog := manager.AgentCatalog()
	if len(catalog) != 1 || catalog[0].Online || catalog[0].Hostname != "WS01" || catalog[0].Nickname != "File server relay" || catalog[0].Privilege != "high" || len(catalog[0].AdvertisedRoutes) != 1 || catalog[0].DisconnectedAt.IsZero() {
		t.Fatalf("catalog=%+v", catalog)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := restarted.SetOperationsStore(reopened); err != nil {
		t.Fatal(err)
	}
	catalog = restarted.AgentCatalog()
	if len(catalog) != 1 || catalog[0].Online || catalog[0].Hostname != "WS01" || catalog[0].Nickname != "File server relay" || catalog[0].Privilege != "high" {
		t.Fatalf("restored=%+v", catalog)
	}
	topology := restarted.Topology()
	for _, node := range topology.Nodes {
		if node.ID == "agent:agent-record" && (node.Label != "File server relay" || node.Privilege != "high") {
			t.Fatalf("retained topology agent=%+v", node)
		}
	}
	found := false
	for _, node := range topology.Nodes {
		if node.ID == "agent:agent-record" {
			found = true
			if node.Active {
				t.Fatal("offline topology node active")
			}
		}
	}
	if !found {
		t.Fatal("offline agent missing from topology")
	}
}
