//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agentprofile"
)

func TestGeneratedPowerShellDeploymentAgainstSelfSignedHTTPS(t *testing.T) {
	payload := []byte("@echo off\r\nexit /b 0\r\n")
	hash := sha256.Sum256(payload)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/opaque-download" {
			http.NotFound(w, r)
			return
		}
		w.Write(payload)
	}))
	defer server.Close()

	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			if shell == "powershell.exe" {
				t.Fatalf("Windows PowerShell 5.1 is required for this regression test: %v", err)
			}
			continue
		}
		t.Run(shell, func(t *testing.T) {
			for _, validHash := range []bool{true, false} {
				name := "valid hash"
				if !validHash {
					name = "wrong hash"
				}
				t.Run(name, func(t *testing.T) {
					sha := hex.EncodeToString(hash[:])
					if !validHash {
						sha = strings.Repeat("0", 64)
					}
					hosted := hostedArtifactInfo{
						Artifact:      agentprofile.Artifact{Filename: "helper.cmd", SHA256: sha},
						Retrieval:     server.URL + "/opaque-download",
						TLSSelfSigned: true,
					}
					var script bytes.Buffer
					if err := printDeployScript(&script, hosted, "powershell"); err != nil {
						t.Fatal(err)
					}
					// This also catches a process-global certificate bypass.
					script.WriteString("\nif ([System.Net.ServicePointManager]::ServerCertificateValidationCallback -ne $null) { throw 'global TLS callback changed' }\nWrite-Output 'DEPLOY_HELPER_OK'\n")
					dir := t.TempDir()
					path := filepath.Join(dir, "deploy.ps1")
					dest := filepath.Join(dir, "downloaded.cmd")
					if err := os.WriteFile(path, script.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path, "-Destination", dest)
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil {
						t.Fatalf("helper timed out: %s", out)
					}
					if validHash {
						if err != nil || !bytes.Contains(out, []byte("DEPLOY_HELPER_OK")) {
							t.Fatalf("self-signed download failed: %v\n%s", err, out)
						}
						got, readErr := os.ReadFile(dest)
						if readErr != nil || !bytes.Equal(got, payload) {
							t.Fatalf("downloaded bytes: %v %q", readErr, got)
						}
					} else {
						if err == nil || !bytes.Contains(out, []byte("Artifact SHA-256 mismatch")) {
							t.Fatalf("wrong hash was not rejected: %v\n%s", err, out)
						}
						if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
							t.Fatalf("destination exists after hash failure: %v", statErr)
						}
					}
				})
			}
		})
	}
}
