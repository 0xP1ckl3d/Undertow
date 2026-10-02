//go:build wasip1

package main

import (
	"fmt"
	"os"
	"strings"
	"undertow/modules/wasm/hostapi"
)

func main() {
	var system map[string]any
	if err := hostapi.Call("system", nil, &system); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Startup and autorun audit: %v\n", system["hostname"])
	if startup, err := hostapi.Text("startup", nil); err == nil {
		fmt.Println(firstLines(startup, 45))
	} else {
		fmt.Println(err)
	}
	if system["os"] == "windows" {
		for _, key := range []string{`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, `HKLM\Software\Microsoft\Windows\CurrentVersion\RunOnce`} {
			text, err := hostapi.Text("registry.read", map[string]any{"key": key})
			if err == nil {
				fmt.Printf("[%s]\n%s\n", key, firstLines(text, 25))
			}
		}
	} else {
		path := fmt.Sprint(system["home"]) + "/.config/autostart"
		var entries []map[string]any
		if hostapi.Call("fs.walk", map[string]any{"path": path, "depth": 1}, &entries) == nil {
			for _, entry := range entries {
				fmt.Printf("user autorun: %v\n", entry["path"])
			}
		}
	}
}

func firstLines(value string, max int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > max {
		lines = append(lines[:max], "[remaining lines omitted]")
	}
	return strings.Join(lines, "\n")
}
