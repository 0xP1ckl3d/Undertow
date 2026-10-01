//go:build windows

package pivot

import (
	"context"
	"errors"
	"strings"
)

func wasmPlatformInventory(ctx context.Context, op string) (any, error) {
	switch op {
	case "processes":
		return inventoryCommand(ctx, "tasklist.exe", "/fo", "csv")
	case "privileges":
		return platformHostInfo(ctx, "privileges")
	case "routes":
		return platformHostInfo(ctx, "route-table")
	case "dns_config":
		return platformHostInfo(ctx, "dns")
	case "users":
		return inventoryCommand(ctx, "net.exe", "user")
	case "groups":
		return inventoryCommand(ctx, "net.exe", "localgroup")
	case "neighbours":
		return inventoryCommand(ctx, "arp.exe", "-a")
	case "connections":
		return inventoryCommand(ctx, "netstat.exe", "-ano")
	case "services":
		return inventoryCommand(ctx, "sc.exe", "query", "state=", "all")
	case "startup":
		return inventoryCommand(ctx, "reg.exe", "query", `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`)
	}
	return nil, errWASMUnsupported
}

func wasmRegistryRead(ctx context.Context, req wasmHostRequest) (any, error) {
	key := strings.ToUpper(req.Key)
	if !(strings.HasPrefix(key, `HKLM\`) || strings.HasPrefix(key, `HKCU\`) || strings.HasPrefix(key, `HKEY_LOCAL_MACHINE\`) || strings.HasPrefix(key, `HKEY_CURRENT_USER\`)) || len(req.Key) > 1024 || strings.ContainsAny(req.Key, "\r\n\x00") {
		return nil, errors.New("invalid registry key")
	}
	args := []string{"query", req.Key}
	if req.Name != "" {
		if len(req.Name) > 256 || strings.ContainsAny(req.Name, "\r\n\x00") {
			return nil, errors.New("invalid registry value name")
		}
		args = append(args, "/v", req.Name)
	}
	return inventoryCommand(ctx, "reg.exe", args...)
}
