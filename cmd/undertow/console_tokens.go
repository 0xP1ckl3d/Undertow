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

	"undertow/internal/authcontext"
	"undertow/internal/pivot"
)

// Selection flags are consumed by Undertow, never appended to a child command
// line or module arguments. -- protects literal arguments with the same name.
func parseTokenOption(args []string) ([]string, string, error) {
	var id string
	out := make([]string, 0, len(args))
	options := true
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			options = false
			out = append(out, args[i])
			continue
		}
		if options && args[i] == "--token-context" {
			if id != "" || i+1 >= len(args) {
				return nil, "", errors.New("use one --token-context ID|process")
			}
			i++
			id = args[i]
			if id != "process" && !authcontext.ValidID(id) {
				return nil, "", errors.New("invalid authentication context ID")
			}
			continue
		}
		out = append(out, args[i])
	}
	return out, id, nil
}
func runConsoleTokens(ctx context.Context, out io.Writer, call consoleCaller, args []string, agent string) error {
	if agent == "" {
		if len(args) < 3 {
			return errors.New("use tokens AGENT_ID list|discover|import|create|use|revert|remove|clear")
		}
		agent = args[1]
		args = append([]string{args[0]}, args[2:]...)
	}
	if len(args) < 2 {
		return errors.New("use tokens list|discover|import|create|use|revert|remove|clear")
	}
	r := pivot.TokenRequest{Action: args[1]}
	var logon *authcontext.LogonRequest
	switch r.Action {
	case "list", "discover", "revert", "clear":
		if len(args) != 2 {
			return errors.New("unexpected token arguments")
		}
	case "import", "use", "remove":
		if len(args) != 3 || !authcontext.ValidID(args[2]) {
			return errors.New("provide an opaque candidate or context ID")
		}
		r.ID = args[2]
	case "create":
		if len(args) != 6 {
			return errors.New("use tokens create USER DOMAIN PASSWORD_FILE interactive|network|batch|new_credentials (DOMAIN - for UPN)")
		}
		data, err := os.ReadFile(args[4])
		if err != nil {
			return errors.New("cannot read password file")
		}
		defer clear(data)
		if len(data) > 2048 {
			return errors.New("password file exceeds limit")
		}
		domain := args[3]
		if domain == "-" {
			domain = ""
		}
		logon = &authcontext.LogonRequest{User: args[2], Domain: domain, Password: strings.TrimRight(string(data), "\r\n"), LogonType: args[5]}
	default:
		return errors.New("unknown token action")
	}
	if logon != nil {
		sealed, err := sealTokenLogon(ctx, call, agent, *logon)
		logon.Password = ""
		if err != nil {
			return err
		}
		r.SealedLogon = &sealed
	}
	method := http.MethodPost
	var body any = r
	if r.Action == "list" {
		method = http.MethodGet
		body = nil
	}
	data, err := call(ctx, method, "/v1/agents/"+url.PathEscape(agent)+"/tokens", body)

	if err != nil {
		return err
	}
	var response pivot.TokenResponse
	if err = json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.Created != nil {
		fmt.Fprintf(out, "Created context %s (%s)\n", response.Created.ID, response.Created.Identity)
	}
	if response.DefaultContextID != "" {
		fmt.Fprintf(out, "This operator session default: %s\n", response.DefaultContextID)
	} else {
		fmt.Fprintln(out, "This operator session default: agent process identity")
	}
	items := response.Contexts
	if r.Action == "discover" {
		items = response.Candidates
		fmt.Fprintln(out, "Candidates expire after two minutes or the next discovery.")
	}
	for _, m := range items {
		fmt.Fprintf(out, "%s  %s  domain=%s user=%s type=%s impersonation=%s integrity=%s session=%d elevated=%t elevation=%s source=%s created=%s\n", m.ID, m.Identity, m.Domain, m.User, m.TokenType, m.ImpersonationLevel, m.IntegrityLevel, m.SessionID, m.Elevated, m.ElevationType, m.Source, m.CreatedAt.Format("2006-01-02T15:04:05Z"))
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "No contexts returned.")
	}
	return nil
}

func sealTokenLogon(ctx context.Context, call consoleCaller, agent string, r authcontext.LogonRequest) (authcontext.SealedLogon, error) {
	data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agent)+"/tokens", pivot.TokenRequest{Action: "creation-key"})
	if err != nil {
		return authcontext.SealedLogon{}, err
	}
	var response pivot.TokenResponse
	if json.Unmarshal(data, &response) != nil || response.CreationKey == nil {
		return authcontext.SealedLogon{}, errors.New("agent did not provide a live creation key")
	}
	return authcontext.SealLogon(*response.CreationKey, r)
}
