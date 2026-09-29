package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"undertow/internal/pivot"
)

type interactiveOpener func(context.Context, string, pivot.InteractiveRequest) (*pivot.InteractiveSession, error)

func runInteractiveConsole(ctx context.Context, output io.Writer, editor *consoleEditor, open interactiveOpener, agentID string, argv []string) error {
	if editor == nil {
		return errors.New("interactive sessions require a terminal")
	}
	input, detach := editor.beginInteractive()
	defer editor.endInteractive()
	cols, rows := consoleSize(os.Stdout)
	session, err := open(ctx, agentID, pivot.InteractiveRequest{Argv: argv, Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	defer session.Close()
	fmt.Fprintln(output, "\r\nInteractive session. Press Ctrl-] to exit this shell.")
	finished := make(chan error, 1)
	go func() {
		for {
			kind, data, err := session.Read()
			if err != nil {
				finished <- err
				return
			}
			switch kind {
			case pivot.InteractiveOutput:
				_, _ = output.Write(data)
			case pivot.InteractiveExit:
				if len(data) == 4 {
					code := int32(binary.BigEndian.Uint32(data))
					if code == 0 {
						finished <- nil
					} else {
						finished <- fmt.Errorf("exit status %d", code)
					}
					return
				}
				finished <- errors.New("invalid interactive exit status")
				return
			case pivot.InteractiveError:
				finished <- errors.New(string(data))
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case b := <-input:
			batch := []byte{b}
		forMore:
			for len(batch) < 512 {
				select {
				case next := <-input:
					batch = append(batch, next)
				default:
					break forMore
				}
			}
			if err := session.Send(batch); err != nil {
				return err
			}
		case <-detach:
			fmt.Fprintln(output, "\r\nInteractive session closed.")
			return nil
		case err := <-finished:
			return err
		case <-ticker.C:
			newCols, newRows := consoleSize(os.Stdout)
			if newCols != cols || newRows != rows {
				cols, rows = newCols, newRows
				_ = session.Resize(cols, rows)
			}
		case <-ctx.Done():
			return nil
		}
	}
}
