package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"undertow/internal/control"
)

func runConsoleOperators(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	usage := errors.New("use operators list | create ID DISPLAY_NAME operator|team_leader PASSWORD_FILE | role ID operator|team_leader | disable ID | enable ID | reset ID PASSWORD_FILE")
	if len(args) < 2 {
		return usage
	}
	path := func(id string) string { return "/v1/operators/" + url.PathEscape(id) }
	switch args[1] {
	case "me":
		if len(args) != 2 {
			return usage
		}
		data, err := call(ctx, http.MethodGet, "/v1/operator/me", nil)
		if err != nil {
			return err
		}
		var a control.OperatorAccount
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s (%s) · %s\n", a.DisplayName, a.ID, a.Role)
		return nil
	case "list":
		if len(args) != 2 {
			return usage
		}
		data, err := call(ctx, http.MethodGet, "/v1/operators", nil)
		if err != nil {
			return err
		}
		var accounts []control.OperatorAccount
		if err := json.Unmarshal(data, &accounts); err != nil {
			return err
		}
		for _, a := range accounts {
			state := "active"
			if a.Revoked {
				state = "revoked"
			} else if a.Disabled {
				state = "disabled"
			}
			fmt.Fprintf(output, "%-24s %-16s %-9s %s\n", a.ID, a.Role, state, a.DisplayName)
		}
		return nil
	case "create":
		if len(args) != 6 {
			return usage
		}
		password, err := operatorPasswordFromFile(args[5])
		if err != nil {
			return err
		}
		_, err = call(ctx, http.MethodPost, "/v1/operators", map[string]any{"id": args[2], "display_name": args[3], "role": args[4], "password": password})
		if err == nil {
			fmt.Fprintln(output, "Operator created")
		}
		return err
	case "role":
		if len(args) != 4 {
			return usage
		}
		_, err := call(ctx, http.MethodPut, path(args[2]), map[string]any{"role": args[3]})
		return err
	case "disable", "enable":
		if len(args) != 3 {
			return usage
		}
		_, err := call(ctx, http.MethodPut, path(args[2]), map[string]any{"disabled": args[1] == "disable"})
		return err
	case "reset":
		if len(args) != 4 {
			return usage
		}
		password, err := operatorPasswordFromFile(args[3])
		if err != nil {
			return err
		}
		_, err = call(ctx, http.MethodPut, path(args[2]), map[string]any{"password": password})
		return err
	case "revoke":
		if len(args) != 3 {
			return usage
		}
		_, err := call(ctx, http.MethodDelete, path(args[2]), nil)
		return err
	}
	return usage
}

func operatorPasswordFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read operator password: %w", err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}
