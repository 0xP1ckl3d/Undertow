package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"undertow/internal/pivot"
)

func fetchScreens(ctx context.Context, call consoleCaller, agentID string) ([]pivot.ScreenInfo, error) {
	data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/exec", pivot.ExecRequest{Builtin: "screens"})
	if err != nil {
		return nil, err
	}
	var result pivot.ExecResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.QueuedJobID != "" {
		return nil, fmt.Errorf("screen list queued for next check-in as job %s; inspect it in Jobs", result.QueuedJobID)
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	screens := result.Screens
	if screens == nil {
		if err := json.Unmarshal([]byte(result.Stdout), &screens); err != nil {
			return nil, fmt.Errorf("invalid agent screen list: %w", err)
		}
	}
	return screens, nil
}

func runConsoleScreens(ctx context.Context, output io.Writer, call consoleCaller, agentID string) error {
	screens, err := fetchScreens(ctx, call, agentID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "screen list queued for next check-in") {
			fmt.Fprintln(output, err.Error())
			return nil
		}
		return err
	}
	fmt.Fprintf(output, "Screens (%d):\n", len(screens))
	for _, screen := range screens {
		foreground := screen.Foreground
		if foreground == "" {
			foreground = "[no foreground app]"
		}
		fmt.Fprintf(output, "  %d  %s  %dx%d  %s\n", screen.Number, screen.Name, screen.Width, screen.Height, foreground)
	}
	return nil
}

func runConsoleScreenshot(ctx context.Context, output io.Writer, call consoleCaller, transfer clientTransferAction, agentID string, args []string) error {
	if transfer == nil {
		return errors.New("screenshots require the VPN client console")
	}
	if agentID == "" || strings.ContainsAny(agentID, "/\\") || agentID == "." || agentID == ".." {
		return errors.New("invalid agent ID for output path")
	}
	number := 0
	outputDir := filepath.Join("outputs", "screenshots")
	for i := 0; i < len(args); i++ {
		if args[i] == "--output" && i+1 < len(args) {
			outputDir = args[i+1]
			i++
		} else if n, err := strconv.Atoi(args[i]); err == nil && n > 0 && number == 0 {
			number = n
		} else {
			return errors.New("use screenshot [NUMBER] [--output DIRECTORY]")
		}
	}
	var screens []pivot.ScreenInfo
	if number != 0 {
		// The explicit screen number can be submitted while an agent sleeps.
		// The agent validates it when the transfer starts at check-in.
		screens = []pivot.ScreenInfo{{Number: number}}
	} else {
		var err error
		screens, err = fetchScreens(ctx, call, agentID)
		if err != nil {
			return err
		}
	}
	if err := prepareClientOutputDirectory(outputDir); err != nil {
		return err
	}
	for _, screen := range screens {
		if number != 0 && screen.Number != number {
			continue
		}
		filename := fmt.Sprintf("%s-screen-%d-%s.png", shortAgentID(agentID), screen.Number, time.Now().UTC().Format("20060102T150405.000000000Z"))
		local, err := filepath.Abs(filepath.Join(outputDir, filename))
		if err != nil {
			return err
		}
		result, err := transfer(ctx, clientFileRequest{AgentID: agentID, Operation: "screenshot", RemotePath: strconv.Itoa(screen.Number), LocalPath: local}, nil)
		if err != nil {
			return fmt.Errorf("screen %d: %w", screen.Number, err)
		}
		if !result.OK || !strings.EqualFold(filepath.Ext(local), ".png") {
			return errors.New("invalid screenshot transfer result")
		}
		fmt.Fprintf(output, "Screen %d saved to %s (%d bytes, SHA-256 %s)\n", screen.Number, local, result.Size, result.SHA256)
	}
	return nil
}
