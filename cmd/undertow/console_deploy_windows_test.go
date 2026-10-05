//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/namedpipe"
	"undertow/internal/transport/tlscert"
)

func TestGeneratedPowerShellDeploymentExplainsUnreachableRelay(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			var script bytes.Buffer
			hosted := hostedArtifactInfo{Artifact: agentprofile.Artifact{Filename: "helper.cmd", SHA256: strings.Repeat("a", 64)}, Retrieval: "https://" + address + "/" + strings.Repeat("b", 48), TLSSelfSigned: true, TLSCertSHA256: strings.Repeat("c", 64)}
			if err := printDeployScript(&script, hosted, "powershell"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "deploy.ps1")
			if err := os.WriteFile(path, script.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil || err == nil || !bytes.Contains(out, []byte("Cannot connect to payload listener "+address)) || !bytes.Contains(out, []byte("resolves to the parent agent")) {
				t.Fatalf("unreachable relay diagnostic: %v, %v\n%s", err, ctx.Err(), out)
			}
		})
	}
}

func TestGeneratedPowerShellRelayDiagnosticUsesHEADOnly(t *testing.T) {
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hash := strings.Repeat("b", 64)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/"+token {
			http.Error(w, "unexpected diagnostic request", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Artifact-SHA256", hash)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	certificate := sha256.Sum256(server.Certificate().Raw)
	hosted := hostedArtifactInfo{Artifact: agentprofile.Artifact{SHA256: hash}, Retrieval: server.URL + "/" + token, RetrievalPath: "/" + token, TLSCertSHA256: hex.EncodeToString(certificate[:])}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			var script bytes.Buffer
			if err := printAgentHostProbeScript(&script, hosted, "powershell"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "diagnose.ps1")
			if err := os.WriteFile(path, script.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("No payload was downloaded or started")) {
				t.Fatalf("relay diagnostic: %v\n%s", err, output)
			}
		})
	}
}

func TestGeneratedPowerShellPipeDiagnosticUsesHEADOnly(t *testing.T) {
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hash := strings.Repeat("b", 64)
	certificate, err := tlscert.Load("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	certHash := sha256.Sum256(certificate.Leaf.Raw)
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			id, err := agentprofile.ID()
			if err != nil {
				t.Fatal(err)
			}
			pipeName := "undertow-diagnostic-" + id
			listener, err := namedpipe.Listen(`\\.\pipe\` + pipeName)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				defer tlsConn.Close()
				_ = tlsConn.SetDeadline(time.Now().Add(20 * time.Second))
				request, err := http.ReadRequest(bufio.NewReader(tlsConn))
				if err != nil {
					serverDone <- err
					return
				}
				if request.Method != http.MethodHead || request.URL.Path != "/"+token {
					serverDone <- fmt.Errorf("unexpected diagnostic request: %s %s", request.Method, request.URL.Path)
					return
				}
				_, err = fmt.Fprintf(tlsConn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nX-Artifact-SHA256: %s\r\nConnection: close\r\n\r\n", hash)
				serverDone <- err
			}()
			var script bytes.Buffer
			hosted := hostedArtifactInfo{Artifact: agentprofile.Artifact{SHA256: hash}, PipePath: `\\.\pipe\` + pipeName, RetrievalPath: "/" + token, TLSCertSHA256: hex.EncodeToString(certHash[:])}
			if err := printAgentHostProbeScript(&script, hosted, "powershell"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "diagnose-pipe.ps1")
			if err := os.WriteFile(path, script.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("No payload was downloaded or started")) {
				t.Fatalf("pipe diagnostic: %v\n%s", err, output)
			}
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatal(serverErr)
			}
		})
	}
}

func TestGeneratedPowerShellDeploymentOverSMBPipe(t *testing.T) {
	payload := []byte("@echo off\r\nexit /b 0\r\n")
	hash := sha256.Sum256(payload)
	certificate, err := tlscert.Load("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	certHash := sha256.Sum256(certificate.Leaf.Raw)
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			if shell == "powershell.exe" {
				t.Fatal(err)
			}
			continue
		}
		t.Run(shell, func(t *testing.T) {
			for _, validPin := range []bool{true, false} {
				name := "valid pin"
				if !validPin {
					name = "wrong pin"
				}
				t.Run(name, func(t *testing.T) {
					id, err := agentprofile.ID()
					if err != nil {
						t.Fatal(err)
					}
					pipeName := "undertow-test-" + id
					listener, err := namedpipe.Listen(`\\.\pipe\` + pipeName)
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					serverDone := make(chan error, 1)
					go func() {
						conn, err := listener.Accept()
						if err != nil {
							serverDone <- err
							return
						}
						tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
						defer tlsConn.Close()
						_ = tlsConn.SetDeadline(time.Now().Add(20 * time.Second))
						request, err := http.ReadRequest(bufio.NewReader(tlsConn))
						if err != nil {
							serverDone <- err
							return
						}
						if request.Method != http.MethodGet || request.URL.Path != "/"+strings.Repeat("a", 48) {
							serverDone <- fmt.Errorf("unexpected pipe request: %s %s", request.Method, request.URL.Path)
							return
						}
						_, err = fmt.Fprintf(tlsConn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nX-Artifact-SHA256: %x\r\nConnection: close\r\n\r\n%s", len(payload), hash, payload)
						serverDone <- err
					}()
					pin := hex.EncodeToString(certHash[:])
					if !validPin {
						pin = strings.Repeat("0", 64)
					}
					var script bytes.Buffer
					err = printDeployScript(&script, hostedArtifactInfo{Artifact: agentprofile.Artifact{Filename: "helper.cmd", SHA256: hex.EncodeToString(hash[:])}, PipePath: `\\.\pipe\` + pipeName, RetrievalPath: "/" + strings.Repeat("a", 48), TLSCertSHA256: pin}, "powershell")
					if err != nil {
						t.Fatal(err)
					}
					script.WriteString("\nif ([System.Net.ServicePointManager]::ServerCertificateValidationCallback -ne $null) { throw 'global TLS callback changed' }\nWrite-Output 'PIPE_HELPER_OK'\n")
					dir := t.TempDir()
					path := filepath.Join(dir, "deploy.ps1")
					if err := os.WriteFile(path, script.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					var serverErr error
					select {
					case serverErr = <-serverDone:
					case <-time.After(2 * time.Second):
						t.Fatal("pipe server did not finish")
					}
					if ctx.Err() != nil {
						t.Fatalf("pipe helper timed out: %s", out)
					}
					if validPin {
						if err != nil || !bytes.Contains(out, []byte("PIPE_HELPER_OK")) {
							t.Fatalf("pipe helper failed: %v (server: %v)\n%s", err, serverErr, out)
						}
						got, readErr := os.ReadFile(filepath.Join(dir, "helper.cmd"))
						if readErr != nil || !bytes.Equal(got, payload) {
							t.Fatalf("downloaded bytes: %v %q", readErr, got)
						}
					} else if err == nil {
						t.Fatal("wrong certificate pin was accepted")
					}
				})
			}
		})
	}
}

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
			for _, tc := range []struct {
				name      string
				validHash bool
				pin       bool
			}{
				{name: "default destination in working directory", validHash: true},
				{name: "pinned certificate", validHash: true, pin: true},
				{name: "wrong hash"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					sha := hex.EncodeToString(hash[:])
					if !tc.validHash {
						sha = strings.Repeat("0", 64)
					}
					hosted := hostedArtifactInfo{
						Artifact:      agentprofile.Artifact{Filename: "helper.cmd", SHA256: sha},
						Retrieval:     server.URL + "/opaque-download",
						TLSSelfSigned: true,
					}
					if tc.pin {
						certHash := sha256.Sum256(server.Certificate().Raw)
						hosted.TLSCertSHA256 = hex.EncodeToString(certHash[:])
					}
					var script bytes.Buffer
					if err := printDeployScript(&script, hosted, "powershell"); err != nil {
						t.Fatal(err)
					}
					// This also catches a process-global certificate bypass.
					script.WriteString("\nif ([System.Net.ServicePointManager]::ServerCertificateValidationCallback -ne $null) { throw 'global TLS callback changed' }\nWrite-Output 'DEPLOY_HELPER_OK'\n")
					dir := t.TempDir()
					path := filepath.Join(t.TempDir(), "deploy.ps1")
					dest := filepath.Join(dir, "helper.cmd")
					// Reproduce an operator session that already loaded an older
					// helper type with a different CreateHandler signature.
					const legacyFactory = "Add-Type -TypeDefinition 'public static class ScopedArtifactTls { public static object CreateHandler() { return null; } }'\n[System.Net.WebRequest]::DefaultWebProxy = [System.Net.WebProxy]::new('http://127.0.0.1:9')\n"
					generated := script.Bytes()
					paramEnd := bytes.IndexByte(generated, '\n') + 1
					if paramEnd == 0 {
						t.Fatal("generated script has no param line")
					}
					withLegacyType := make([]byte, 0, len(generated)+len(legacyFactory))
					withLegacyType = append(withLegacyType, generated[:paramEnd]...)
					withLegacyType = append(withLegacyType, legacyFactory...)
					withLegacyType = append(withLegacyType, generated[paramEnd:]...)
					if err := os.WriteFile(path, withLegacyType, 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path}
					if !tc.validHash {
						args = append(args, "-Destination", dest)
					}
					cmd := exec.CommandContext(ctx, shell, args...)
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil {
						t.Fatalf("helper timed out: %s", out)
					}
					if tc.validHash {
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
