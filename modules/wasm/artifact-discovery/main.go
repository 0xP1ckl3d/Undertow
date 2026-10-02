//go:build wasip1

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"undertow/modules/wasm/hostapi"
)

func main() {
	var system map[string]any
	if err := hostapi.Call("system", nil, &system); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := fmt.Sprint(system["home"])
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	patterns := []string{".ssh", ".aws", ".kube", "id_rsa", "credentials", "config", ".env", "unattend", "sysprep"}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if p := strings.TrimSpace(scanner.Text()); p != "" && len(p) < 80 {
			patterns = append(patterns, strings.ToLower(p))
		}
		if len(patterns) >= 32 {
			break
		}
	}
	var entries []map[string]any
	if err := hostapi.Call("fs.walk", map[string]any{"path": root, "depth": 3}, &entries); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Interesting file names under %s (metadata only):\n", root)
	for _, entry := range entries {
		path := fmt.Sprint(entry["path"])
		name := strings.ToLower(filepath.Base(path))
		for _, pattern := range patterns {
			if strings.Contains(name, pattern) {
				fmt.Printf("%s mode=%v size=%v\n", path, entry["mode"], entry["size"])
				break
			}
		}
	}
}
