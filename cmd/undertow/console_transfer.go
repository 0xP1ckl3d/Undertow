package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"undertow/internal/pivot"
)

func runConsoleTransfer(ctx context.Context, output io.Writer, editor *consoleEditor, transfer clientTransferAction, args []string) error {
	if len(args) != 4 {
		return errors.New("use upload AGENT_ID LOCAL REMOTE or download AGENT_ID REMOTE LOCAL")
	}
	request := clientFileRequest{AgentID: args[1], Operation: args[0]}
	if args[0] == "upload" {
		request.LocalPath, request.RemotePath = args[2], args[3]
	} else {
		request.LocalPath, request.RemotePath = args[3], args[2]
	}
	local, err := filepath.Abs(request.LocalPath)
	if err != nil {
		return err
	}
	request.LocalPath = local
	transferCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var detach <-chan struct{}
	if editor != nil {
		_, detach = editor.beginInteractive()
		defer editor.endInteractive()
	}
	label := strings.ToUpper(request.Operation[:1]) + request.Operation[1:]
	fmt.Fprintf(output, "%s in progress", label)
	if editor != nil {
		fmt.Fprint(output, "; press Ctrl-] to cancel")
	}
	fmt.Fprintln(output)
	type outcome struct {
		result pivot.FileMessage
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := transfer(transferCtx, request, func(progress pivot.TransferProgress) {
			fmt.Fprintf(output, "\r%s: %d/%d bytes (%.1f%%), %.1f KiB/s\x1b[K", label, progress.Bytes, progress.Total, progress.Percent, progress.Rate/1024)
		})
		done <- outcome{result, err}
	}()
	select {
	case result := <-done:
		fmt.Fprintln(output)
		if result.err != nil {
			return fmt.Errorf("%s failed after transfer started: %w", label, result.err)
		}
		fmt.Fprintf(output, "%s complete: %d bytes, SHA-256 %s\n", label, result.result.Size, result.result.SHA256)
		return nil
	case <-detach:
		cancel()
		fmt.Fprintf(output, "\nCancelling %s...\n", strings.ToLower(label))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return errors.New("transfer cancelled")
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}
