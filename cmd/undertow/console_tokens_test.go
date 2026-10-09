package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/authcontext"
	"undertow/internal/pivot"
)

func TestTokenConsoleCreatesFromFileWithoutSecretOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(path, []byte("fixture-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var keys authcontext.CreationKeys
	call := func(_ context.Context, method, url string, body any) ([]byte, error) {
		if method != http.MethodPost || url != "/v1/agents/agent/tokens" {
			t.Fatal(method, url)
		}
		r := body.(pivot.TokenRequest)
		if r.Action == "creation-key" {
			key, err := keys.Issue()
			if err != nil {
				return nil, err
			}
			return json.Marshal(pivot.TokenResponse{CreationKey: &key})
		}
		if r.SealedLogon == nil {
			t.Fatal("plaintext logon crossed server boundary")
		}
		sealedData, _ := json.Marshal(r)
		if strings.Contains(string(sealedData), "fixture-secret") {
			t.Fatal("secret in server transport")
		}
		logon, err := keys.Open(*r.SealedLogon)
		if err != nil || logon.Password != "fixture-secret" {
			t.Fatal("agent did not receive sealed logon", err)
		}

		return json.Marshal(pivot.TokenResponse{})
	}
	if err := runConsoleTokens(context.Background(), &out, call, []string{"tokens", "create", "fixture-user", ".", path, "interactive"}, "agent"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "fixture-secret") {
		t.Fatal("secret printed")
	}
}
func TestTokenOptionDoesNotReachExecutableOrModuleArguments(t *testing.T) {
	id := strings.Repeat("a", 32)
	args, selected, err := parseTokenOption([]string{"exec", "--token-context", id, "whoami.exe"})
	if err != nil || selected != id || strings.Join(args, " ") != "exec whoami.exe" {
		t.Fatal(args, selected, err)
	}
	args, selected, err = parseTokenOption([]string{"module", "--", "--token-context", "literal"})
	if err != nil || selected != "" || len(args) != 4 {
		t.Fatal(args, selected, err)
	}
	for _, args := range [][]string{{"exec", "--token-context"}, {"exec", "--token-context", "123"}, {"exec", "--token-context", "process", "--token-context", id}} {
		if _, _, err := parseTokenOption(args); err == nil {
			t.Fatal("invalid flag accepted", args)
		}
	}
}
