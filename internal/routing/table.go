package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
)

type Route struct {
	Prefix  netip.Prefix `json:"prefix"`
	AgentID string       `json:"agent_id"`
	Active  bool         `json:"active"`
}

// Table binds each remote prefix to one agent. Agent loss deactivates routes
// without changing their owner.
type Table struct {
	mu     sync.RWMutex
	routes map[netip.Prefix]Route
	local  []netip.Prefix
}

func New(local []netip.Prefix) *Table {
	return &Table{routes: make(map[netip.Prefix]Route), local: append([]netip.Prefix(nil), local...)}
}

func overlaps(a, b netip.Prefix) bool {
	return a.Addr().Is4() == b.Addr().Is4() && (a.Contains(b.Addr()) || b.Contains(a.Addr()))
}

func (t *Table) Add(prefix netip.Prefix, agentID string) error {
	if !prefix.IsValid() || agentID == "" {
		return errors.New("valid prefix and agent ID required")
	}
	prefix = prefix.Masked()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, local := range t.local {
		if overlaps(prefix, local) {
			return fmt.Errorf("route %s conflicts with local network %s", prefix, local)
		}
	}
	for _, existing := range t.routes {
		if overlaps(prefix, existing.Prefix) {
			return fmt.Errorf("route %s conflicts with %s owned by %s", prefix, existing.Prefix, existing.AgentID)
		}
	}
	t.routes[prefix] = Route{Prefix: prefix, AgentID: agentID}
	return nil
}

func (t *Table) Delete(prefix netip.Prefix) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefix = prefix.Masked()
	if _, ok := t.routes[prefix]; !ok {
		return false
	}
	delete(t.routes, prefix)
	return true
}

func (t *Table) SetAgentActive(agentID string, active bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for p, r := range t.routes {
		if r.AgentID == agentID {
			r.Active = active
			t.routes[p] = r
		}
	}
}

func (t *Table) SetRouteActive(prefix netip.Prefix, active bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefix = prefix.Masked()
	r, ok := t.routes[prefix]
	if !ok {
		return false
	}
	r.Active = active
	t.routes[prefix] = r
	return true
}

func (t *Table) Resolve(ip netip.Addr) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, r := range t.routes {
		if r.Active && r.Prefix.Contains(ip) {
			return r.AgentID, true
		}
	}
	return "", false
}

func (t *Table) Lookup(ip netip.Addr) (Route, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, r := range t.routes {
		if r.Prefix.Contains(ip) {
			return r, true
		}
	}
	return Route{}, false
}

func (t *Table) List() []Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Route, 0, len(t.routes))
	for _, r := range t.routes {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix.String() < out[j].Prefix.String() })
	return out
}
