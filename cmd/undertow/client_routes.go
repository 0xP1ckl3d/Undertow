//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/routing"
	"undertow/internal/tun"
)

type clientRouteDevice interface {
	AddRoute(string) error
	DelRoute(string) error
}

func loadClientRoutes(path string) ([]control.AcceptedRoute, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var routes []control.AcceptedRoute
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, fmt.Errorf("read client routes: %w", err)
	}
	if len(routes) > 64 {
		return nil, errors.New("client routes file exceeds 64 entries")
	}
	seen := make(map[string]bool)
	for i := range routes {
		prefix, err := netip.ParsePrefix(routes[i].Prefix)
		if err != nil || !prefix.Addr().Is4() || routes[i].AgentID == "" {
			return nil, fmt.Errorf("invalid client route %q", routes[i].Prefix)
		}
		routes[i].Prefix = prefix.Masked().String()
		if seen[routes[i].Prefix] {
			return nil, fmt.Errorf("duplicate client route %s", routes[i].Prefix)
		}
		seen[routes[i].Prefix] = true
	}
	return routes, nil
}

func saveClientRoutes(path string, routes []control.AcceptedRoute) error {
	data, err := json.MarshalIndent(routes, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".undertow-routes-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func (c *liveClientConsole) set(session *mux.Mux, id uint64, device *tun.Device, modeRoutes *clientModeRoutes, publicIP string) {
	c.routeMu.Lock()
	c.mu.Lock()
	c.session, c.sessionID = session, id
	c.modeRoutes = modeRoutes
	c.publicIP = publicIP
	if device == nil {
		c.device = nil
	} else {
		c.device = device
	}
	c.active = make(map[string]bool)
	c.global = make(map[string]bool)
	c.mu.Unlock()
	c.routeMu.Unlock()
	if session != nil {
		c.notify("VPN connected")
		if !c.operatorOnly {
			go c.restoreLoop(session)
		}
	} else {
		c.notify("VPN disconnected")
	}
}

func (c *liveClientConsole) restoreLoop(session *mux.Mux) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	lastErrors := make(map[string]string)
	lastGlobalError := ""
	for {
		c.routeMu.Lock()
		c.mu.RLock()
		current := c.session == session
		c.mu.RUnlock()
		if !current {
			c.routeMu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := c.reconcileRouteAvailability(ctx); err != nil {
			log.Printf("accepted route availability pending: %v", err)
		}
		cancel()
		for _, route := range c.routes {
			if route.Disabled {
				continue
			}
			if c.active[route.Prefix] {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := c.activateRoute(ctx, route)
			cancel()
			if err != nil {
				if lastErrors[route.Prefix] != err.Error() {
					log.Printf("accepted route %s via %s pending: %v", route.Prefix, route.AgentID, err)
					lastErrors[route.Prefix] = err.Error()
				}
			} else if c.active[route.Prefix] {
				delete(lastErrors, route.Prefix)
				log.Printf("accepted route active: %s via %s", route.Prefix, route.AgentID)
			} else {
				delete(lastErrors, route.Prefix)
			}
		}
		if !c.vpn {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := c.syncGlobalRoutes(ctx)
			cancel()
			if err != nil && err.Error() != lastGlobalError {
				log.Printf("server route sync pending: %v", err)
				lastGlobalError = err.Error()
			} else if err == nil {
				lastGlobalError = ""
			}
		}
		c.routeMu.Unlock()
		select {
		case <-session.Done():
			return
		case <-ticker.C:
		}
	}
}

// A saved acceptance stays on the server while its agent sleeps, but the OS
// route must not claim traffic until the path can actually carry packets.
// The caller holds routeMu.
func (c *liveClientConsole) reconcileRouteAvailability(ctx context.Context) error {
	data, err := c.call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return err
	}
	var status struct {
		Agents []control.AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	online := make(map[string]bool, len(status.Agents))
	archived := make(map[string]bool, len(status.Agents))
	for _, agent := range status.Agents {
		online[agent.ID] = agent.Online
		archived[agent.ID] = agent.Archived
	}
	for _, route := range c.routes {
		if !c.active[route.Prefix] || online[route.AgentID] {
			continue
		}
		if err := c.device.DelRoute(route.Prefix); err != nil {
			return fmt.Errorf("remove unavailable route %s: %w", route.Prefix, err)
		}
		delete(c.active, route.Prefix)
	}
	for _, route := range append([]control.AcceptedRoute(nil), c.routes...) {
		if archived[route.AgentID] {
			if err := c.removeClientRouteLocked(ctx, route.Prefix, route.AgentID); err != nil {
				return fmt.Errorf("remove archived agent route %s: %w", route.Prefix, err)
			}
		}
	}
	return nil
}

// The caller holds routeMu. Internal-only mode mirrors active server routes
// locally because it has no broad /1 route to send those packets into TUN.
func (c *liveClientConsole) syncGlobalRoutes(ctx context.Context) error {
	data, err := c.call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return err
	}
	return c.applyGlobalRouteStatus(data)
}

// The caller holds routeMu.
func (c *liveClientConsole) applyGlobalRouteStatus(data []byte) error {
	var status struct {
		Routes  []routing.Route      `json:"routes"`
		Clients []control.ClientInfo `json:"clients"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	c.mu.RLock()
	id, device := c.sessionID, c.device
	c.mu.RUnlock()
	if id == 0 || device == nil {
		return errors.New("client is not connected")
	}
	modeFound, internal := false, false
	for _, client := range status.Clients {
		if client.SessionID == id {
			modeFound, internal = true, client.Internal
			break
		}
	}
	if !modeFound {
		return errors.New("client session is not in server status")
	}
	desired := make(map[string]bool)
	if internal {
		for _, route := range status.Routes {
			prefix := route.Prefix.Masked()
			if !route.Active || !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() == 0 || prefix.Contains(c.serverIP) || prefix.Contains(c.carrierIP) || prefix.Contains(c.tunnelPrefix.Addr()) || c.tunnelPrefix.Contains(prefix.Addr()) {
				continue
			}
			conflict := false
			for _, local := range c.localNetworks {
				if prefix.Contains(local.Addr()) || local.Contains(prefix.Addr()) {
					conflict = true
					break
				}
			}
			if !conflict {
				desired[prefix.String()] = true
			}
		}
	}
	for prefix := range c.global {
		if !desired[prefix] {
			if err := device.DelRoute(prefix); err != nil {
				return fmt.Errorf("remove server route %s: %w", prefix, err)
			}
			delete(c.global, prefix)
		}
	}
	for prefix := range desired {
		if c.global[prefix] || c.active[prefix] {
			continue
		}
		if err := device.AddRoute(prefix); err != nil {
			return fmt.Errorf("install server route %s: %w", prefix, err)
		}
		c.global[prefix] = true
		log.Printf("server route active on client: %s", prefix)
	}
	return nil
}

// The caller holds routeMu. The server route is bound to this VPN session only.
func (c *liveClientConsole) activateRoute(ctx context.Context, route control.AcceptedRoute) error {
	c.mu.RLock()
	id, device := c.sessionID, c.device
	c.mu.RUnlock()
	if id == 0 || device == nil {
		return errors.New("VPN client is not connected")
	}
	prefix, err := netip.ParsePrefix(route.Prefix)
	if err != nil || !prefix.Addr().Is4() || prefix.Contains(c.serverIP) || prefix.Contains(c.carrierIP) || prefix.Contains(c.tunnelPrefix.Addr()) || c.tunnelPrefix.Contains(prefix.Addr()) {
		return errors.New("route is invalid or overlaps the server, carrier endpoint, or client tunnel network")
	}
	path := fmt.Sprintf("/v1/clients/%d/routes", id)
	response, err := c.call(ctx, http.MethodPost, path, route)
	if err != nil {
		return err
	}
	connected := true // Compatibility with servers predating the route state reply.
	if len(response) != 0 {
		var state struct {
			Connected *bool `json:"connected"`
		}
		if err := json.Unmarshal(response, &state); err != nil || state.Connected == nil {
			return errors.New("server returned an invalid route state")
		}
		connected = *state.Connected
	}
	if !connected {
		if c.active[route.Prefix] {
			if err := device.DelRoute(route.Prefix); err != nil {
				return fmt.Errorf("remove unavailable local route: %w", err)
			}
			delete(c.active, route.Prefix)
		}
		return nil // Accepted now; install on the next connected callback.
	}
	if c.active[route.Prefix] {
		return nil
	}
	if !c.global[route.Prefix] {
		if err := device.AddRoute(route.Prefix); err != nil {
			_, _ = c.call(ctx, http.MethodDelete, path+"?prefix="+url.QueryEscape(route.Prefix), nil)
			return fmt.Errorf("install local route: %w", err)
		}
	} else {
		delete(c.global, route.Prefix) // Transfer OS route ownership to this accepted route.
	}
	c.active[route.Prefix] = true
	return nil
}

// AcceptClientRoute is shared by the terminal and local GUI. It validates the
// route, binds server acceptance to this client session, installs the OS route
// when the agent is connected, and persists the local choice as one operation.
func (c *liveClientConsole) AcceptClientRoute(ctx context.Context, prefix, agentID string, manual bool) (bool, error) {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	return c.acceptClientRouteLocked(ctx, prefix, agentID, manual)
}

// The caller holds routeMu. The bool reports reassignment from a disconnected agent.
func (c *liveClientConsole) acceptClientRouteLocked(ctx context.Context, rawPrefix, agentID string, manual bool) (bool, error) {
	prefix, err := netip.ParsePrefix(rawPrefix)
	if err != nil || !prefix.Addr().Is4() || prefix.Contains(c.serverIP) || prefix.Contains(c.carrierIP) || prefix.Contains(c.tunnelPrefix.Addr()) || c.tunnelPrefix.Contains(prefix.Addr()) {
		return false, errors.New("route must be IPv4 CIDR outside the server, carrier endpoint, and client tunnel networks")
	}
	prefix = prefix.Masked()
	for _, local := range c.localNetworks {
		if prefix.Overlaps(local) {
			return false, fmt.Errorf("route %s conflicts with local network %s", prefix, local)
		}
	}
	for i, existing := range c.routes {
		if existing.Prefix != prefix.String() {
			continue
		}
		if existing.AgentID == agentID {
			if existing.Disabled {
				return false, c.setClientRouteEnabledLocked(ctx, i, true)
			}
			return false, fmt.Errorf("route %s is already accepted via this agent", prefix)
		}
		agents, err := consoleAgents(ctx, c.call)
		if err != nil {
			return false, fmt.Errorf("check current route owner: %w", err)
		}
		for _, agent := range agents {
			if agent.ID == existing.AgentID {
				return false, fmt.Errorf("route %s is owned by connected agent %s (%s); remove it before assigning another agent", prefix, consoleAgentName(agent), agent.ID)
			}
		}
		if err := c.replaceAcceptedRoute(ctx, i, control.AcceptedRoute{Prefix: prefix.String(), AgentID: agentID, Manual: manual}); err != nil {
			return false, err
		}
		return true, nil
	}
	if len(c.routes) >= 64 {
		return false, errors.New("at most 64 client routes can be saved")
	}
	route := control.AcceptedRoute{Prefix: prefix.String(), AgentID: agentID, Manual: manual}
	if err := c.activateRoute(ctx, route); err != nil {
		return false, err
	}
	updated := append(append([]control.AcceptedRoute(nil), c.routes...), route)
	if err := saveClientRoutes(c.routeFile, updated); err != nil {
		_ = c.removeActiveRoute(ctx, route)
		return false, fmt.Errorf("save client route: %w", err)
	}
	c.routes = updated
	return false, nil
}

func (c *liveClientConsole) RemoveClientRoute(ctx context.Context, prefix, agentID string) error {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	return c.removeClientRouteLocked(ctx, prefix, agentID)
}

func (c *liveClientConsole) SetClientRouteEnabled(ctx context.Context, prefix, agentID string, enabled bool) error {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	parsed, err := netip.ParsePrefix(prefix)
	if err != nil || !parsed.Addr().Is4() {
		return errors.New("route requires an IPv4 CIDR")
	}
	for i, route := range c.routes {
		if route.Prefix == parsed.Masked().String() && route.AgentID == agentID {
			return c.setClientRouteEnabledLocked(ctx, i, enabled)
		}
	}
	return errors.New("saved client route not found")
}

// The caller holds routeMu. Persist only after server and OS route state agree.
func (c *liveClientConsole) setClientRouteEnabledLocked(ctx context.Context, index int, enabled bool) error {
	route := c.routes[index]
	if route.Disabled == !enabled {
		return nil
	}
	if enabled {
		route.Disabled = false
		if err := c.activateRoute(ctx, route); err != nil {
			return err
		}
	} else if err := c.removeActiveRoute(ctx, route); err != nil {
		return err
	}
	updated := append([]control.AcceptedRoute(nil), c.routes...)
	updated[index].Disabled = !enabled
	if err := saveClientRoutes(c.routeFile, updated); err != nil {
		if enabled {
			_ = c.removeActiveRoute(ctx, route)
		} else {
			_ = c.activateRoute(ctx, route)
		}
		return fmt.Errorf("save client route: %w", err)
	}
	c.routes = updated
	return nil
}

// The caller holds routeMu. An empty agentID means any current owner.
func (c *liveClientConsole) removeClientRouteLocked(ctx context.Context, rawPrefix, agentID string) error {
	prefix, err := netip.ParsePrefix(rawPrefix)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("route del requires an IPv4 CIDR")
	}
	key := prefix.Masked().String()
	for i, route := range c.routes {
		if route.Prefix != key {
			continue
		}
		if agentID != "" && route.AgentID != agentID {
			return errors.New("route belongs to a different agent")
		}
		updated := append(append([]control.AcceptedRoute(nil), c.routes[:i]...), c.routes[i+1:]...)
		wasActive := c.active[route.Prefix]
		if err := c.removeActiveRoute(ctx, route); err != nil {
			return err
		}
		if err := saveClientRoutes(c.routeFile, updated); err != nil {
			if wasActive {
				_ = c.activateRoute(ctx, route)
			}
			return err
		}
		c.routes = updated
		return nil
	}
	return errors.New("route is not accepted by this client")
}

func (c *liveClientConsole) routeCommand(ctx context.Context, args []string, output io.Writer) error {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	if len(args) > 0 && args[0] == "vpn" {
		return c.vpnCommand(ctx, args, output)
	}
	if len(args) > 0 && args[0] == "internal" {
		return c.internalCommand(ctx, args, output)
	}
	if (len(args) == 1 || len(args) == 2) && args[0] == "routes" {
		data, err := c.call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		var status struct {
			Agents []control.AgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		fmt.Fprintln(output, "Advertised routes:")
		for _, agent := range status.Agents {
			if len(args) == 2 && agent.ID != args[1] {
				continue
			}
			for _, prefix := range agent.AdvertisedRoutes {
				fmt.Fprintf(output, "  %s via %s\n", prefix, agentRouteLabel(agent.ID, status.Agents))
			}
			if agent.DefaultRoute != nil {
				fmt.Fprintf(output, "  default via %s on %s (informational)\n", agent.DefaultRoute.Gateway, agent.DefaultRoute.Interface)
			}
			for _, route := range agent.Routes {
				prefix, err := netip.ParsePrefix(route.Prefix)
				if err != nil {
					continue
				}
				conflict := false
				for _, local := range c.localNetworks {
					if prefix.Overlaps(local) {
						conflict = true
						break
					}
				}
				kind := "direct"
				if !route.Direct {
					kind = "routed via " + route.Gateway
				}
				if conflict {
					kind += "; overlaps a local route"
				}
				fmt.Fprintf(output, "  candidate %s via %s\n", route.Prefix, agentRouteLabel(agent.ID, status.Agents))
				fmt.Fprintf(output, "    %s; interface=%s; source=%s\n", kind, route.Interface, route.Source)
			}
		}
		fmt.Fprintln(output, "Accepted local routes:")
		for _, route := range c.routes {
			if len(args) == 2 && route.AgentID != args[1] {
				continue
			}
			kind := "advertised"
			if route.Manual {
				kind = "manual"
			}
			fmt.Fprintf(output, "  %s via %s\n", route.Prefix, agentRouteLabel(route.AgentID, status.Agents))
			fmt.Fprintf(output, "    %s; enabled=%t; active=%t\n", kind, !route.Disabled, c.active[route.Prefix])
		}
		return nil
	}
	if len(args) == 4 && args[0] == "route" && (args[1] == "accept" || args[1] == "add") {
		previousOwner := ""
		for _, route := range c.routes {
			if route.Prefix == args[2] {
				previousOwner = route.AgentID
				break
			}
		}
		reassigned, err := c.acceptClientRouteLocked(ctx, args[2], args[3], args[1] == "add")
		if err != nil {
			return err
		}
		if reassigned {
			fmt.Fprintf(output, "Local route %s reassigned from %s to %s and saved.\n", args[2], previousOwner, args[3])
		} else {
			fmt.Fprintf(output, "Local route %s via %s accepted and saved.\n", args[2], args[3])
		}
		return nil
	}
	if (len(args) == 3 || len(args) == 4) && args[0] == "route" && (args[1] == "del" || args[1] == "delete") {
		agentID := ""
		if len(args) == 4 {
			agentID = args[3]
		}
		if err := c.removeClientRouteLocked(ctx, args[2], agentID); err != nil {
			return err
		}
		fmt.Fprintf(output, "Local route %s removed.\n", args[2])
		return nil
	}
	return errors.New("use routes, route accept CIDR AGENT_ID, route add CIDR AGENT_ID, or route del CIDR (delete is an alias)")
}

// The caller holds routeMu. Reuse an installed local route while changing its
// server-side owner, and restore the saved route if the new owner is rejected.
func (c *liveClientConsole) replaceAcceptedRoute(ctx context.Context, index int, replacement control.AcceptedRoute) error {
	previous := c.routes[index]
	updated := append([]control.AcceptedRoute(nil), c.routes...)
	updated[index] = replacement
	if err := saveClientRoutes(c.routeFile, updated); err != nil {
		return fmt.Errorf("save client route: %w", err)
	}
	var err error
	err = c.activateRoute(ctx, replacement)
	if err != nil {
		if c.active[previous.Prefix] {
			_ = c.activateRoute(ctx, previous)
		}
		if rollbackErr := saveClientRoutes(c.routeFile, c.routes); rollbackErr != nil {
			return fmt.Errorf("reassign route: %w (restore saved route: %v)", err, rollbackErr)
		}
		return err
	}
	c.routes = updated
	return nil
}

// The caller holds routeMu.
func (c *liveClientConsole) removeActiveRoute(ctx context.Context, route control.AcceptedRoute) error {
	c.mu.RLock()
	id, device := c.sessionID, c.device
	c.mu.RUnlock()
	if id != 0 {
		path := fmt.Sprintf("/v1/clients/%d/routes?prefix=%s", id, url.QueryEscape(route.Prefix))
		if _, err := c.call(ctx, http.MethodDelete, path, nil); err != nil {
			return err
		}
	}
	if device != nil && c.active[route.Prefix] {
		if err := device.DelRoute(route.Prefix); err != nil {
			if id != 0 {
				path := fmt.Sprintf("/v1/clients/%d/routes", id)
				_, _ = c.call(ctx, http.MethodPost, path, route)
			}
			return err
		}
	}
	delete(c.active, route.Prefix)
	return nil
}
