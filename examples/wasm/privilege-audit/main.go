//go:build wasip1

package main

import (
	"fmt"
	"os"
	"strings"
	"undertow/examples/wasm/hostapi"
)

func main() {
	var system map[string]any
	if err := hostapi.Call("system", nil, &system); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Privilege and configuration audit: %v\n", system["hostname"])
	if privileges, err := hostapi.Text("privileges", nil); err == nil {
		fmt.Println(privileges)
	}
	paths := []string{"/etc/sudoers", "/etc/passwd", "/etc/shadow", "/etc/systemd/system", "/usr/local/bin"}
	if system["os"] == "windows" {
		paths = []string{`C:\Windows\System32\config`, `C:\ProgramData`, `C:\Windows\Tasks`}
	}
	for _, path := range paths {
		var stat map[string]any
		if err := hostapi.Call("fs.stat", map[string]any{"path": path}, &stat); err != nil {
			continue
		}
		fmt.Printf("%s mode=%v size=%v\n", path, stat["mode"], stat["size"])
		if permissions, ok := stat["permissions"].(float64); ok && int(permissions)&0002 != 0 {
			fmt.Printf("  review: world writable %s\n", path)
		}
	}
	if system["os"] == "linux" {
		var entries []map[string]any
		if err := hostapi.Call("fs.walk", map[string]any{"path": "/etc/systemd/system", "depth": 1}, &entries); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(fmt.Sprint(entry["path"]), ".service") && int(entry["permissions"].(float64))&0002 != 0 {
					fmt.Printf("  review: writable service unit %v\n", entry["path"])
				}
			}
		}
	}
}
