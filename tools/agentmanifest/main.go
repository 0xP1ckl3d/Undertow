package main

import (
	"fmt"
	"os"

	"undertow/internal/agentprofile"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: agentmanifest DIRECTORY UNDERTOW_BUILD_VERSION [TEMPLATE ...]")
		os.Exit(2)
	}
	names := []string{"undertow-agent-windows-amd64.exe", "undertow-agent-linux-amd64", "undertow-agent-linux-arm64"}
	if len(os.Args) > 3 {
		names = os.Args[3:]
	}
	if err := agentprofile.WriteTemplateManifest(os.Args[1], os.Args[2], names); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
