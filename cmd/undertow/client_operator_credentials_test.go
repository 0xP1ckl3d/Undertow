//go:build linux || windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientOperatorCredentialSources(t *testing.T) {
	t.Setenv(operatorIDEnv, "alice")
	t.Setenv(operatorPasswordEnv, "an environment password")
	credentials, err := resolveClientOperatorCredentials("", "")
	if err != nil || credentials.ID != "alice" || credentials.Password != "an environment password" {
		t.Fatalf("env credentials=%+v error=%v", credentials, err)
	}
	path := filepath.Join(t.TempDir(), "operator.password")
	if err := os.WriteFile(path, []byte("a password from file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(operatorPasswordEnv, "")
	credentials, err = resolveClientOperatorCredentials("bob", path)
	if err != nil || credentials.ID != "bob" || credentials.Password != "a password from file" {
		t.Fatalf("file credentials=%+v error=%v", credentials, err)
	}
	t.Setenv(operatorPasswordEnv, "conflict password")
	if _, err := resolveClientOperatorCredentials("bob", path); err == nil {
		t.Fatal("file and environment both accepted")
	}
	t.Setenv(operatorPasswordEnv, "")
	if _, err := resolveClientOperatorCredentials("bob", "", false); err == nil {
		t.Fatal("noninteractive launch accepted an empty environment password")
	}
}

func TestBootstrapOperatorFromEnvironment(t *testing.T) {
	t.Setenv(operatorIDEnv, "leader")
	t.Setenv(operatorPasswordEnv, "a unique strong password")
	path := filepath.Join(t.TempDir(), "operations.db")
	if err := operatorAccountsCommand([]string{"bootstrap", "--operations-db", path}); err != nil {
		t.Fatal(err)
	}
	if err := operatorAccountsCommand([]string{"bootstrap", "--operations-db", path}); err == nil {
		t.Fatal("second bootstrap succeeded")
	}
}
