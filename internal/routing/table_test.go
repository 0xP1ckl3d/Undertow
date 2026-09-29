package routing

import (
	"net/netip"
	"testing"
)

func TestOwnershipCollisionAndDisconnect(t *testing.T) {
	table := New([]netip.Prefix{netip.MustParsePrefix("172.16.254.0/24"), netip.MustParsePrefix("192.168.1.0/24")})
	if err := table.Add(netip.MustParsePrefix("10.20.0.0/16"), "agent-a"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"10.20.1.0/24", "10.0.0.0/8", "172.16.254.0/24", "192.168.1.4/32"} {
		if err := table.Add(netip.MustParsePrefix(p), "agent-b"); err == nil {
			t.Fatalf("accepted collision %s", p)
		}
	}
	table.SetAgentActive("agent-a", true)
	if id, ok := table.Resolve(netip.MustParseAddr("10.20.1.50")); !ok || id != "agent-a" {
		t.Fatalf("resolve: %q %v", id, ok)
	}
	table.SetAgentActive("agent-a", false)
	if _, ok := table.Resolve(netip.MustParseAddr("10.20.1.50")); ok {
		t.Fatal("disconnected route remained active")
	}
	if len(table.List()) != 1 {
		t.Fatal("route ownership lost on disconnect")
	}
	if !table.Delete(netip.MustParsePrefix("10.20.0.0/16")) {
		t.Fatal("delete failed")
	}
}
