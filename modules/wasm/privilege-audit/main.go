//go:build wasip1

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"undertow/modules/wasm/hostapi"
)

type systemInfo struct {
	OS, Hostname, User string
}

type serviceConfig struct {
	Name              string `json:"name"`
	ImagePath         string `json:"image_path"`
	ExpandedImagePath string `json:"expanded_image_path"`
	Account           string `json:"account"`
	Type              uint64 `json:"type"`
	Start             uint64 `json:"start"`
}

var regValueLine = regexp.MustCompile(`(?i)^\s*([\w]+)\s+REG_\w+\s+(.+?)\s*$`)
var registryAttempted, registryReadable int

func main() {
	var system systemInfo
	if err := hostapi.Call("system", nil, &system); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Privilege and configuration audit: %s (%s, %s)\n", system.Hostname, system.OS, system.User)
	if system.OS != "windows" {
		checkUnix(system.OS)
		return
	}
	if privileges, err := hostapi.Text("privileges", nil); err == nil {
		for _, privilege := range []string{"SeImpersonatePrivilege", "SeAssignPrimaryTokenPrivilege", "SeDebugPrivilege", "SeBackupPrivilege", "SeRestorePrivilege"} {
			if strings.Contains(strings.ToLower(privileges), strings.ToLower(privilege)) {
				fmt.Printf("[context] Token lists %s; inspect its enabled state and account context\n", privilege)
			}
		}
	}
	checkPolicies()
	checkServices()
}

func checkUnix(platform string) {
	if platform != "linux" {
		fmt.Println("No privilege checks are implemented for this OS")
		return
	}
	if privileges, err := hostapi.Text("privileges", nil); err == nil {
		fmt.Println(privileges)
	}
	type fileInfo struct {
		Path        string `json:"path"`
		Mode        string `json:"mode"`
		Size        int64  `json:"size"`
		Permissions uint32 `json:"permissions"`
	}
	for _, path := range []string{"/etc/sudoers", "/etc/passwd", "/etc/shadow", "/etc/systemd/system", "/usr/local/bin"} {
		var info fileInfo
		if err := hostapi.Call("fs.stat", map[string]any{"path": path}, &info); err != nil {
			continue
		}
		fmt.Printf("%s mode=%s size=%d\n", path, info.Mode, info.Size)
		if info.Permissions&0002 != 0 {
			fmt.Printf("[review] World-writable system path: %s\n", path)
		}
	}
	var units []fileInfo
	if err := hostapi.Call("fs.walk", map[string]any{"path": "/etc/systemd/system", "depth": 1}, &units); err == nil {
		for _, unit := range units {
			if strings.HasSuffix(unit.Path, ".service") && unit.Permissions&0002 != 0 {
				fmt.Printf("[review] World-writable service unit: %s\n", unit.Path)
			}
		}
	}
}

func registryValue(key, name string) (string, bool) {
	registryAttempted++
	output, err := hostapi.Text("registry.read", map[string]any{"key": key, "name": name})
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(output, "\n") {
		parts := regValueLine.FindStringSubmatch(line)
		if len(parts) == 3 && strings.EqualFold(parts[1], name) {
			registryReadable++
			return strings.TrimSpace(parts[2]), true
		}
	}
	return "", false
}

func isOne(value string) bool { return strings.EqualFold(value, "0x1") || value == "1" }

func checkPolicies() {
	machine, machineOK := registryValue(`HKLM\SOFTWARE\Policies\Microsoft\Windows\Installer`, "AlwaysInstallElevated")
	user, userOK := registryValue(`HKCU\SOFTWARE\Policies\Microsoft\Windows\Installer`, "AlwaysInstallElevated")
	if machineOK && userOK && isOne(machine) && isOne(user) {
		fmt.Println("[finding] AlwaysInstallElevated is enabled in both HKLM and HKCU")
	} else if machineOK && isOne(machine) {
		fmt.Println("[context] AlwaysInstallElevated is enabled in HKLM only; both hives are required")
	}
	if value, ok := registryValue(`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`, "AutoAdminLogon"); ok && isOne(value) {
		fmt.Println("[review] Automatic logon is enabled; inspect credential storage locally")
	}
	if value, ok := registryValue(`HKLM\SYSTEM\CurrentControlSet\Control\Lsa`, "RunAsPPL"); ok {
		fmt.Printf("[context] LSA protection RunAsPPL=%s\n", value)
	}
	if value, ok := registryValue(`HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\WDigest`, "UseLogonCredential"); ok && isOne(value) {
		fmt.Println("[review] WDigest UseLogonCredential is enabled")
	}
	if value, ok := registryValue(`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "EnableLUA"); ok && (value == "0" || strings.EqualFold(value, "0x0")) {
		fmt.Println("[review] UAC EnableLUA is disabled")
	}
	updatePolicy := `HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate`
	updateAU := updatePolicy + `\AU`
	server, serverOK := registryValue(updatePolicy, "WUServer")
	useServer, useOK := registryValue(updateAU, "UseWUServer")
	if serverOK && useOK && isOne(useServer) && strings.HasPrefix(strings.ToLower(server), "http://") {
		fmt.Println("[review] WSUS is configured with an HTTP update server")
	}
	fmt.Printf("Policy scan: %d/%d registry values readable; absent or inaccessible values omitted\n", registryReadable, registryAttempted)
}

func checkServices() {
	var names []string
	const pageSize = 128
	for offset := 0; ; offset += pageSize {
		var page []string
		if err := hostapi.Call("windows.service_names", map[string]any{"offset": offset, "limit": pageSize}, &page); err != nil {
			fmt.Printf("[unavailable] Service inventory at offset %d: %v\n", offset, err)
			return
		}
		names = append(names, page...)
		if len(page) < pageSize {
			break
		}
	}
	checked, candidates, unavailable := 0, 0, 0
	for _, name := range names {
		var service serviceConfig
		if err := hostapi.Call("windows.service_config", map[string]any{"name": name}, &service); err != nil {
			unavailable++
			continue
		}
		// Win32 own-process and shared-process services, excluding kernel drivers.
		if service.Type&0x30 == 0 || service.ImagePath == "" {
			continue
		}
		checked++
		if !strings.EqualFold(service.Account, "LocalSystem") && !strings.EqualFold(service.Account, `NT AUTHORITY\SYSTEM`) {
			continue
		}
		probes := unquotedPathProbes(service.ExpandedImagePath)
		if len(probes) == 0 {
			continue
		}
		candidates++
		fmt.Printf("[review] SYSTEM service %q has an unquoted image path: %s\n", name, service.ImagePath)
		for _, path := range probes {
			fmt.Printf("  possible search path: %s\n", path)
			if acl, err := hostapi.Text("windows.file_acl", map[string]any{"path": parent(path)}); err == nil {
				fmt.Printf("  parent ACL: %s\n", compactACL(acl))
			} else {
				fmt.Printf("  parent ACL unavailable: %v\n", err)
			}
		}
	}
	fmt.Printf("Service scan: %d names, %d Win32 services inspected, %d unquoted SYSTEM candidates, %d inaccessible\n", len(names), checked, candidates, unavailable)
}
