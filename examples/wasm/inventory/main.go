//go:build wasip1

package main

import (
	"fmt"
	"os"
	"strings"
	"undertow/examples/wasm/hostapi"
)

func main() {
	filter := ""
	if len(os.Args) > 1 {
		filter = strings.ToLower(os.Args[1])
	}
	fmt.Println("Process, service and network inventory")
	for _, op := range []string{"processes", "services", "connections", "neighbours"} {
		text, err := hostapi.Text(op, nil)
		if err != nil {
			fmt.Printf("%s: %v\n", op, err)
			continue
		}
		fmt.Printf("\n[%s]\n", op)
		count := 0
		for _, line := range strings.Split(text, "\n") {
			if filter != "" && !strings.Contains(strings.ToLower(line), filter) {
				continue
			}
			if count >= 30 {
				fmt.Println("[remaining lines omitted]")
				break
			}
			fmt.Println(line)
			count++
		}
	}
}
