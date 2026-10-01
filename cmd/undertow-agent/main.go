package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/bof"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "_bof-worker" {
		if err := bof.WorkerMain(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "undertow-agent accepts no command-line arguments")
		os.Exit(2)
	}
	path, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	embedded, err := agentprofile.Read(path)
	if err != nil {
		log.Fatal(err)
	}
	cfg := embedded.Profile.Config
	cfg.Metadata = embedded.Identity()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := agent.Run(ctx, cfg, nil); err != nil {
		log.Fatal(err)
	}
}
