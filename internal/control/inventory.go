package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// SendInventory reports candidate routes; clients must accept them locally.
func SendInventory(ctx context.Context, streamMux *mux.Mux, explicit []string, caps pivot.Capabilities) error {
	return SendInventoryWithIdentity(ctx, streamMux, explicit, caps, ArtifactIdentity{})
}

// ArtifactIdentity describes the configured binary, never its enrollment secret.
type ArtifactIdentity struct {
	ProfileID         string `json:"profile_id,omitempty"`
	Profile           string `json:"profile,omitempty"`
	ArtifactID        string `json:"artifact_id,omitempty"`
	UndertowVersion   string `json:"undertow_version,omitempty"`
	ReconnectPolicy   string `json:"reconnect_policy,omitempty"`
	ReconnectAttempts uint32 `json:"reconnect_attempts,omitempty"`
}

func SendInventoryWithIdentity(ctx context.Context, streamMux *mux.Mux, explicit []string, caps pivot.Capabilities, identity ArtifactIdentity) error {
	return SendInventoryWithPolicy(ctx, streamMux, explicit, caps, identity, SleepPolicy{})
}

func SendInventoryWithPolicy(ctx context.Context, streamMux *mux.Mux, explicit []string, caps pivot.Capabilities, identity ArtifactIdentity, sleep SleepPolicy) error {
	hostname, _ := os.Hostname()
	info := struct {
		Hostname         string                 `json:"hostname"`
		OS               string                 `json:"os"`
		Arch             string                 `json:"arch"`
		Privilege        string                 `json:"privilege,omitempty"`
		Interfaces       []string               `json:"interfaces,omitempty"`
		AdvertisedRoutes []string               `json:"advertised_routes,omitempty"`
		Capabilities     pivot.CapabilityReport `json:"capabilities"`
		Sleep            SleepPolicy            `json:"sleep"`
		SleepSupported   bool                   `json:"sleep_supported"`
		ArtifactIdentity
	}{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, Privilege: currentPrivilege(), Capabilities: caps.Report(), Sleep: sleep, SleepSupported: true, ArtifactIdentity: identity}
	seen := make(map[string]bool)
	for _, raw := range explicit {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() {
			return fmt.Errorf("invalid advertised IPv4 route %q", raw)
		}
		value := prefix.Masked().String()
		if !seen[value] {
			info.AdvertisedRoutes = append(info.AdvertisedRoutes, value)
			seen[value] = true
		}
	}
	explicitCount := len(info.AdvertisedRoutes)
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			info.Interfaces = append(info.Interfaces, iface.Name+"="+addr.String())
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil && prefix.Addr().Is4() && !prefix.Addr().IsLinkLocalUnicast() {
				value := prefix.Masked().String()
				if !seen[value] && len(info.AdvertisedRoutes) < 24 {
					info.AdvertisedRoutes = append(info.AdvertisedRoutes, value)
					seen[value] = true
				}
			}
			if len(info.Interfaces) >= 16 {
				break
			}
		}
		if len(info.Interfaces) >= 16 {
			break
		}
	}
	if !caps.Pivot {
		info.AdvertisedRoutes = nil
		explicitCount = 0
	}
	for {
		data, err := json.Marshal(info)
		if err != nil {
			return err
		}
		if len(data) <= 700 {
			if err := streamMux.SendControl(ctx, data); err != nil {
				return err
			}
			return sendNetworkRouteInventory(ctx, streamMux)
		}
		if len(info.Interfaces) == 0 {
			if len(info.AdvertisedRoutes) <= explicitCount {
				return errors.New("explicit advertised routes exceed inventory size")
			}
			info.AdvertisedRoutes = info.AdvertisedRoutes[:len(info.AdvertisedRoutes)-1]
			continue
		}
		info.Interfaces = info.Interfaces[:len(info.Interfaces)-1]
	}
}

type networkRouteUpdate struct {
	RouteUpdate  bool           `json:"route_update"`
	Reset        bool           `json:"reset,omitempty"`
	Routes       []NetworkRoute `json:"routes,omitempty"`
	DefaultRoute *NetworkRoute  `json:"default_route,omitempty"`
}

func sendNetworkRouteInventory(ctx context.Context, streamMux *mux.Mux) error {
	routes, defaultRoute := collectNetworkRoutes()
	update := networkRouteUpdate{RouteUpdate: true, Reset: true, DefaultRoute: defaultRoute}
	for _, route := range routes {
		update.Routes = append(update.Routes, route)
		encoded, err := json.Marshal(update)
		if err != nil {
			return err
		}
		if len(encoded) <= 700 {
			continue
		}
		update.Routes = update.Routes[:len(update.Routes)-1]
		encoded, err = json.Marshal(update)
		if err != nil {
			return err
		}
		if err := streamMux.SendControl(ctx, encoded); err != nil {
			return err
		}
		update = networkRouteUpdate{RouteUpdate: true, Routes: []NetworkRoute{route}}
		encoded, err = json.Marshal(update)
		if err != nil || len(encoded) > 700 {
			return errors.New("network route entry exceeds inventory frame")
		}
	}
	encoded, err := json.Marshal(update)
	if err != nil {
		return err
	}
	return streamMux.SendControl(ctx, encoded)
}
