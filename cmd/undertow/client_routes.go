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
		go c.restoreLoop(session)
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
		for _, route := range c.routes {
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
			} else {
				delete(lastErrors, route.Prefix)
				log.Printf("accepted route active: %s via %s", route.Prefix, route.AgentID)
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
	if _, err := c.call(ctx, http.MethodPost, path, route); err != nil {
		return err
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
			fmt.Fprintf(output, "    %s; active=%t\n", kind, c.active[route.Prefix])
		}
		return nil
	}
	if len(args) == 4 && args[0] == "route" && (args[1] == "accept" || args[1] == "add") {
		prefix, err := netip.ParsePrefix(args[2])
		if err != nil || !prefix.Addr().Is4() || prefix.Contains(c.serverIP) || prefix.Contains(c.carrierIP) || prefix.Contains(c.tunnelPrefix.Addr()) || c.tunnelPrefix.Contains(prefix.Addr()) {
			return errors.New("route must be IPv4 CIDR outside the server, carrier endpoint, and client tunnel networks")
		}
		prefix = prefix.Masked()
		for _, local := range c.localNetworks {
			if prefix.Overlaps(local) {
				return fmt.Errorf("route %s conflicts with local network %s", prefix, local)
			}
		}
		for i, existing := range c.routes {
			if existing.Prefix == prefix.String() {
				if existing.AgentID == args[3] {
					return fmt.Errorf("route %s is already accepted via this agent", prefix)
				}
				agents, err := consoleAgents(ctx, c.call)
				if err != nil {
					return fmt.Errorf("check current route owner: %w", err)
				}
				for _, agent := range agents {
					if agent.ID == existing.AgentID {
						return fmt.Errorf("route %s is owned by connected agent %s (%s); remove it with route del %s before assigning another agent", prefix, consoleAgentName(agent), agent.ID, prefix)
					}
				}
				replacement := control.AcceptedRoute{Prefix: prefix.String(), AgentID: args[3], Manual: args[1] == "add"}
				if err := c.replaceAcceptedRoute(ctx, i, replacement); err != nil {
					return err
				}
				fmt.Fprintf(output, "Local route %s reassigned from %s to %s and saved.\n", prefix, existing.AgentID, replacement.AgentID)
				return nil
			}
		}
		if len(c.routes) >= 64 {
			return errors.New("at most 64 client routes can be saved")
		}
		route := control.AcceptedRoute{Prefix: prefix.String(), AgentID: args[3], Manual: args[1] == "add"}
		if err := c.activateRoute(ctx, route); err != nil {
			return err
		}
		updated := append(append([]control.AcceptedRoute(nil), c.routes...), route)
		if err := saveClientRoutes(c.routeFile, updated); err != nil {
			_ = c.removeActiveRoute(ctx, route)
			return fmt.Errorf("save client route: %w", err)
		}
		c.routes = updated
		label := route.AgentID
		if data, err := c.call(ctx, http.MethodGet, "/v1/status", nil); err == nil {
			var status struct {
				Agents []control.AgentInfo `json:"agents"`
			}
			if json.Unmarshal(data, &status) == nil {
				label = agentRouteLabel(route.AgentID, status.Agents)
			}
		}
		fmt.Fprintf(output, "Local route %s via %s accepted and saved.\n", route.Prefix, label)
		return nil
	}
	if (len(args) == 3 || len(args) == 4) && args[0] == "route" && (args[1] == "del" || args[1] == "delete") {
		prefix, err := netip.ParsePrefix(args[2])
		if err != nil || !prefix.Addr().Is4() {
			return errors.New("route del requires an IPv4 CIDR")
		}
		key := prefix.Masked().String()
		for i, route := range c.routes {
			if route.Prefix != key {
				continue
			}
			if len(args) == 4 && route.AgentID != args[3] {
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
			fmt.Fprintf(output, "Local route %s removed.\n", key)
			return nil
		}
		return errors.New("route is not accepted by this client")
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
	if c.active[previous.Prefix] {
		c.mu.RLock()
		id := c.sessionID
		c.mu.RUnlock()
		if id == 0 {
			err = errors.New("VPN client is not connected")
		} else {
			path := fmt.Sprintf("/v1/clients/%d/routes", id)
			_, err = c.call(ctx, http.MethodPost, path, replacement)
		}
	} else {
		err = c.activateRoute(ctx, replacement)
	}
	if err != nil {
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
	if !c.active[route.Prefix] {
		return nil
	}
	c.mu.RLock()
	id, device := c.sessionID, c.device
	c.mu.RUnlock()
	if id != 0 {
		path := fmt.Sprintf("/v1/clients/%d/routes?prefix=%s", id, url.QueryEscape(route.Prefix))
		if _, err := c.call(ctx, http.MethodDelete, path, nil); err != nil {
			return err
		}
	}
	if device != nil {
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
