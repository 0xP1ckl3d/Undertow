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

func runConsoleCredentials(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	usage := errors.New("use credentials list | add LABEL USER DOMAIN password|nt_hash SECRET_FILE [shared] | replace ID LABEL USER DOMAIN password|nt_hash SECRET_FILE [shared] | remove ID")
	if len(args) < 2 {
		return usage
	}
	switch args[1] {
	case "list":
		if len(args) != 2 {
			return usage
		}
		data, err := call(ctx, http.MethodGet, "/v1/credentials", nil)
		if err != nil {
			return err
		}
		var records []control.CredentialRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		for _, item := range records {
			fmt.Fprintf(output, "%s  %s  %s  %s  owner=%s shared=%t\n", item.ID, item.Label, item.Account(), item.Kind, item.OwnerID, item.Shared)
		}
		if len(records) == 0 {
			fmt.Fprintln(output, "No stored credentials available.")
		}
		return nil
	case "add", "replace":
		index := 2
		id := ""
		if args[1] == "replace" {
			if len(args) < 3 {
				return usage
			}
			id = args[2]
			index++
		}
		if len(args) != index+5 && len(args) != index+6 {
			return usage
		}
		secret, err := os.ReadFile(args[index+4])
		if err != nil {
			return errors.New("cannot read credential secret file")
		}
		defer clear(secret)
		if len(secret) > 2048 {
			return errors.New("credential secret file exceeds limit")
		}
		shared := len(args) == index+6 && args[index+5] == "shared"
		if len(args) == index+6 && !shared {
			return usage
		}
		domain := args[index+2]
		if domain == "-" {
			domain = ""
		}
		input := control.CredentialInput{Label: args[index], Username: args[index+1], Domain: domain, Kind: args[index+3], Secret: strings.TrimRight(string(secret), "\r\n"), Shared: shared}
		method, path := http.MethodPost, "/v1/credentials"
		if id != "" {
			method, path = http.MethodPut, "/v1/credentials/"+url.PathEscape(id)
		}
		data, err := call(ctx, method, path, input)
		input.Secret = ""
		if err != nil {
			return err
		}
		var record control.CredentialRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Credential %s saved for %s.\n", record.ID, record.Account())
		return nil
	case "remove":
		if len(args) != 3 {
			return usage
		}
		_, err := call(ctx, http.MethodDelete, "/v1/credentials/"+url.PathEscape(args[2]), nil)
		if err == nil {
			fmt.Fprintf(output, "Credential %s removed.\n", args[2])
		}
		return err
	default:
		return usage
	}
}
