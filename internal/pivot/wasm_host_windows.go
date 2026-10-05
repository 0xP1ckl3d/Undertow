//go:build windows

package pivot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func wasmPlatformInventory(ctx context.Context, op string) (any, error) {
	switch op {
	case "processes":
		return windowsProcessInventory(ctx)
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

// These read-only primitives keep the guest independent of Windows APIs and
// avoid parsing a truncated, locale-dependent `sc query` listing.
func wasmWindowsAudit(ctx context.Context, op string, req wasmHostRequest) (any, error) {
	switch op {
	case "windows.service_names":
		if req.Offset < 0 {
			return nil, errors.New("invalid offset")
		}
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.READ)
		if err != nil {
			return nil, err
		}
		defer key.Close()
		names, err := key.ReadSubKeyNames(-1)
		if err != nil {
			return nil, err
		}
		sort.Strings(names)
		if req.Offset >= int64(len(names)) {
			return []string{}, nil
		}
		limit := req.Limit
		if limit <= 0 || limit > 256 {
			limit = 128
		}
		start := int(req.Offset)
		end := start + limit
		if end > len(names) {
			end = len(names)
		}
		return names[start:end], nil
	case "windows.service_config":
		if req.Name == "" || len(req.Name) > 256 || strings.ContainsAny(req.Name, `\/`+"\r\n\x00") || req.Name == "." || req.Name == ".." {
			return nil, errors.New("invalid service name")
		}
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+req.Name, registry.READ)
		if err != nil {
			return nil, err
		}
		defer key.Close()
		image, _, _ := key.GetStringValue("ImagePath")
		account, _, _ := key.GetStringValue("ObjectName")
		if account == "" {
			account = "LocalSystem"
		}
		typ, _, _ := key.GetIntegerValue("Type")
		start, _, _ := key.GetIntegerValue("Start")
		return map[string]any{"name": req.Name, "image_path": image, "expanded_image_path": expandWindowsEnvironment(image), "account": account, "type": typ, "start": start}, nil
	case "windows.file_acl":
		path, err := wasmPath(req.Path)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n*?") {
			return nil, errors.New("invalid path")
		}
		return inventoryCommand(ctx, "icacls.exe", filepath.Clean(path))
	}
	return nil, errWASMUnsupported
}

var percentEnvironment = regexp.MustCompile(`%([^%]+)%`)

func expandWindowsEnvironment(value string) string {
	return percentEnvironment.ReplaceAllStringFunc(value, func(reference string) string {
		if expanded, ok := os.LookupEnv(reference[1 : len(reference)-1]); ok {
			return expanded
		}
		return reference
	})
}
