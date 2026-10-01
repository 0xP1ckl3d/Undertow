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
	"sort"
	"sync"
	"time"

	"undertow/internal/control"
	"undertow/internal/routing"
)

var internetRoutePrefixes = [...]string{"0.0.0.0/1", "128.0.0.0/1"}

// clientModeRoutes owns only Undertow's two Internet routes. The carrier pin
// and accepted internal routes have separate lifetimes.
type clientModeRoutes struct {
	mu      sync.Mutex
	device  clientRouteDevice
	enabled bool
	closed  bool
}

func (r *clientModeRoutes) set(enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("VPN session is disconnected")
	}
	if r.enabled == enabled {
		return nil
	}
	if enabled {
		if err := r.device.AddRoute(internetRoutePrefixes[0]); err != nil {
			return fmt.Errorf("install VPN route %s: %w", internetRoutePrefixes[0], err)
		}
		if err := r.device.AddRoute(internetRoutePrefixes[1]); err != nil {
			_ = r.device.DelRoute(internetRoutePrefixes[0])
			return fmt.Errorf("install VPN route %s: %w", internetRoutePrefixes[1], err)
		}
		r.enabled = true
		return nil
	}
	if err := r.device.DelRoute(internetRoutePrefixes[1]); err != nil {
		return fmt.Errorf("remove VPN route %s: %w", internetRoutePrefixes[1], err)
	}
	if err := r.device.DelRoute(internetRoutePrefixes[0]); err != nil {
		if restoreErr := r.device.AddRoute(internetRoutePrefixes[1]); restoreErr != nil {
			return fmt.Errorf("remove VPN route %s: %w (also failed to restore %s: %v)", internetRoutePrefixes[0], err, internetRoutePrefixes[1], restoreErr)
		}
		return fmt.Errorf("remove VPN route %s: %w", internetRoutePrefixes[0], err)
	}
	r.enabled = false
	return nil
}

func (r *clientModeRoutes) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if r.enabled {
		for i := len(internetRoutePrefixes) - 1; i >= 0; i-- {
			if err := r.device.DelRoute(internetRoutePrefixes[i]); err != nil {
				log.Printf("remove VPN route %s during shutdown: %v", internetRoutePrefixes[i], err)
			}
		}
		r.enabled = false
	}
}

// The caller holds routeMu, so this command cannot race accepted route
// changes or a reconnect replacing the tunnel device.
func (c *liveClientConsole) vpnCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 2 || len(args) == 2 && args[1] != "on" && args[1] != "off" && args[1] != "status" {
		return errors.New("use vpn on, vpn off, or vpn status")
	}
	c.mu.RLock()
	enabled, modeRoutes, verifyURL := c.vpn, c.modeRoutes, c.verifyURL
	transport, publicIP, sessionID := c.transport, c.publicIP, c.sessionID
	c.mu.RUnlock()
	if len(args) == 1 || args[1] == "status" {
		state := "off"
		if enabled {
			state = "on"
		}
		fmt.Fprintf(output, "Internet egress through Undertow: %s\n", state)
		if transport != "" {
			fmt.Fprintf(output, "VPN transport: %s\n", transport)
		}
		if sessionID == 0 {
			fmt.Fprintln(output, "VPN connection: disconnected")
		} else if enabled {
			if publicIP != "" {
				fmt.Fprintf(output, "Public egress verified: %s\n", publicIP)
			} else {
				fmt.Fprintln(output, "Public egress: not verified")
			}
		}
		return nil
	}
	if modeRoutes == nil {
		return errors.New("VPN client is not connected")
	}
	want := args[1] == "on"
	if want == enabled {
		fmt.Fprintf(output, "Internet egress is already %s.\n", args[1])
		return nil
	}
	verificationAddress := ""
	publicIP = ""
	if want && verifyURL != "" {
		resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		address, err := resolveVerificationTarget(resolveCtx, verifyURL)
		cancel()
		if err != nil {
			return fmt.Errorf("resolve public egress check: %w", err)
		}
		verificationAddress = address
	}
	if err := modeRoutes.set(want); err != nil {
		return err
	}
	if want && verifyURL != "" {
		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		verifiedIP, err := verifyPublicIP(verifyCtx, verifyURL, verificationAddress)
		cancel()
		if err != nil {
			if rollbackErr := modeRoutes.set(false); rollbackErr != nil {
				return fmt.Errorf("public egress check failed: %w; rollback failed: %v", err, rollbackErr)
			}
			return fmt.Errorf("public egress check failed; VPN routes rolled back: %w", err)
		}
		publicIP = verifiedIP
		fmt.Fprintf(output, "Public egress verified: %s\n", publicIP)
	}
	c.mu.Lock()
	c.vpn = want
	c.publicIP = publicIP
	c.mu.Unlock()
	fmt.Fprintf(output, "Internet egress through Undertow: %s\n", args[1])
	if !want {
		if err := c.syncGlobalRoutes(ctx); err != nil {
			fmt.Fprintf(output, "Internal route sync pending: %v\n", err)
		}
	}
	return nil
}

// The caller holds routeMu. The chosen mode is kept locally so reconnects
// start with the most recent console setting rather than the original flag.
func (c *liveClientConsole) internalCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 2 || len(args) == 2 && args[1] != "on" && args[1] != "off" && args[1] != "status" {
		return errors.New("use internal on, internal off, or internal status")
	}
	c.mu.RLock()
	id, enabled := c.sessionID, c.internal
	c.mu.RUnlock()
	if len(args) == 1 || args[1] == "status" {
		state := "off"
		if enabled {
			state = "on"
		}
		fmt.Fprintf(output, "Internal agent routing: %s\n", state)
		if id == 0 {
			fmt.Fprintln(output, "Routes: unavailable while disconnected")
			return nil
		}
		data, err := c.call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return fmt.Errorf("fetch current routes: %w", err)
		}
		var status struct {
			Agents []control.AgentInfo `json:"agents"`
			Routes []routing.Route     `json:"routes"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		c.printInternalRoutes(output, status.Agents, status.Routes)
		return nil
	}
	if id == 0 {
		return errors.New("VPN client is not connected")
	}
	want := args[1] == "on"
	if want == enabled {
		fmt.Fprintf(output, "Internal agent routing is already %s.\n", args[1])
		return nil
	}
	if _, err := c.call(ctx, http.MethodPost, fmt.Sprintf("/v1/clients/%d/internal", id), map[string]bool{"enabled": want}); err != nil {
		return err
	}
	c.mu.Lock()
	c.internal = want
	c.mu.Unlock()
	fmt.Fprintf(output, "Internal agent routing: %s for new flows.\n", args[1])
	if err := c.syncGlobalRoutes(ctx); err != nil {
		fmt.Fprintf(output, "Internal route sync pending: %v\n", err)
	}
	return nil
}

// The caller holds routeMu, so active and global reflect installed routes.
func (c *liveClientConsole) printInternalRoutes(output io.Writer, agents []control.AgentInfo, serverRoutes []routing.Route) {
	byPrefix := make(map[string]string)
	for _, route := range c.routes {
		if c.active[route.Prefix] {
			byPrefix[route.Prefix] = route.AgentID
		}
	}
	for _, route := range serverRoutes {
		prefix := route.Prefix.Masked().String()
		if (route.Active && c.internal && c.vpn) || c.global[prefix] {
			if _, exists := byPrefix[prefix]; !exists {
				byPrefix[prefix] = route.AgentID
			}
		}
	}
	prefixes := make([]string, 0, len(byPrefix))
	for prefix := range byPrefix {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	fmt.Fprintln(output, "Routes:")
	if len(prefixes) == 0 {
		fmt.Fprintln(output, "  (none)")
	}
	for _, prefix := range prefixes {
		fmt.Fprintf(output, "  %s via %s\n", prefix, agentRouteLabel(byPrefix[prefix], agents))
	}
}
