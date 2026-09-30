package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func showServerLogs(ctx context.Context, output io.Writer, path string, follow bool, lines <-chan string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	start := info.Size() - 64<<10
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	recent, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return err
	}
	if start > 0 {
		if i := strings.IndexByte(string(recent), '\n'); i >= 0 {
			recent = recent[i+1:]
		}
	}
	entries := strings.Split(strings.TrimRight(string(recent), "\n"), "\n")
	if len(entries) > 40 {
		entries = entries[len(entries)-40:]
	}
	fmt.Fprintf(output, "Recent server logs (%s):\n", path)
	for _, entry := range entries {
		if entry != "" {
			fmt.Fprintln(output, entry)
		}
	}
	if !follow {
		return nil
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	fmt.Fprintln(output, "Following logs. Press Enter or type q to return to the console.")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-lines:
			return nil
		case <-ticker.C:
			data, err := io.ReadAll(io.LimitReader(file, 64<<10))
			if err != nil {
				return err
			}
			if len(data) > 0 {
				_, _ = output.Write(data)
			}
		}
	}
}
