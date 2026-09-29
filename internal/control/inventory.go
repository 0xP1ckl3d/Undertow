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
)

// SendInventory reports candidate routes; clients must accept them locally.
func SendInventory(ctx context.Context, streamMux *mux.Mux, explicit []string) error {
	hostname, _ := os.Hostname()
	info := struct {
		Hostname         string   `json:"hostname"`
		OS               string   `json:"os"`
		Arch             string   `json:"arch"`
		Interfaces       []string `json:"interfaces,omitempty"`
		AdvertisedRoutes []string `json:"advertised_routes,omitempty"`
	}{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH}
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
	for {
		data, err := json.Marshal(info)
		if err != nil {
			return err
		}
		if len(data) <= 700 {
			return streamMux.SendControl(ctx, data)
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
