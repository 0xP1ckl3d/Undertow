package control

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"

	"undertow/internal/mux"
)

// SendInventory reports a bounded, informational agent inventory. Routes are
// never activated merely because an agent advertises an interface.
func SendInventory(ctx context.Context, streamMux *mux.Mux) error {
	hostname, _ := os.Hostname()
	info := struct {
		Hostname   string   `json:"hostname"`
		OS         string   `json:"os"`
		Arch       string   `json:"arch"`
		Interfaces []string `json:"interfaces,omitempty"`
	}{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			info.Interfaces = append(info.Interfaces, iface.Name+"="+addr.String())
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
			return streamMux.SendControl(ctx, []byte(`{"os":"`+runtime.GOOS+`","arch":"`+runtime.GOARCH+`"}`))
		}
		info.Interfaces = info.Interfaces[:len(info.Interfaces)-1]
	}
}
