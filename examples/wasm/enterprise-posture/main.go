//go:build wasip1

package main

import (
	"fmt"
	"os"
	"undertow/examples/wasm/hostapi"
)

func main() {
	var system map[string]any
	if err := hostapi.Call("system", nil, &system); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Enterprise posture: %v (%v)\n", system["hostname"], system["os"])
	for _, name := range []string{"USERDOMAIN", "LOGONSERVER", "USERDNSDOMAIN", "COMPUTERNAME", "KRB5_CONFIG"} {
		var value map[string]any
		if hostapi.Call("environment", map[string]any{"name": name}, &value) == nil && value["exists"] == true {
			fmt.Printf("%s=%v\n", name, value["value"])
		}
	}
	if dns, err := hostapi.Text("dns_config", nil); err == nil {
		fmt.Printf("DNS configuration:\n%.4096s\n", dns)
	}
	if system["os"] == "windows" {
		for _, key := range []string{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, `HKLM\SYSTEM\CurrentControlSet\Control\Lsa`} {
			if value, err := hostapi.Text("registry.read", map[string]any{"key": key}); err == nil {
				fmt.Printf("[%s]\n%.4096s\n", key, value)
			}
		}
	} else {
		for _, path := range []string{"/etc/krb5.conf", "/etc/sssd/sssd.conf", "/etc/samba/smb.conf"} {
			var stat map[string]any
			if hostapi.Call("fs.stat", map[string]any{"path": path}, &stat) == nil {
				fmt.Printf("%s mode=%v size=%v\n", path, stat["mode"], stat["size"])
			}
		}
	}
}
