//go:build wasip1

package main

import (
	"fmt"
	"os"
	"undertow/modules/wasm/hostapi"
)

func main() {
	var info map[string]any
	if err := hostapi.Call("system", nil, &info); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Host triage: %v (%v/%v)\n", info["hostname"], info["os"], info["arch"])
	fmt.Printf("User: %v uid=%v gid=%v\n", info["user"], info["uid"], info["gid"])
	if text, err := hostapi.Text("privileges", nil); err == nil {
		fmt.Println("Privilege context:\n" + text)
	}
}
