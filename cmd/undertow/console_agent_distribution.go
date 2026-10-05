package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"undertow/internal/deployment"
)

func runConsoleAgentDistribution(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) < 2 {
		return errors.New("agent commands manage connected agents: use agents, agent events AGENT_ID, or agent shutdown AGENT_ID; use payload for deployment binaries")
	}
	if args[1] != "show" && isPayloadSubcommand(args[1]) {
		legacy := append([]string{"payload"}, args[1:]...)
		return runConsolePayload(ctx, out, call, legacy)
	}
	if args[1] != "shutdown" && args[1] != "events" {
		return fmt.Errorf("unknown connected-agent command %q; use agents for live sessions or payload help for deployment", args[1])
	}
	if len(args) != 3 {
		return fmt.Errorf("use agent %s AGENT_NUMBER|AGENT_ID|HOSTNAME, or select a connected agent first", args[1])
	}
	if len(args[2]) == 24 {
		if _, err := hex.DecodeString(args[2]); err == nil {
			return fmt.Errorf("%s looks like a payload ID; use payload show %s for the build, or agents to find its connected agent ID", args[2], args[2])
		}
	}
	agents, err := consoleAgents(ctx, call)
	if err != nil {
		return err
	}
	a, err := findConsoleAgent(agents, args[2])
	id := a.ID
	if err != nil {
		if args[1] != "events" || len(args[2]) != 32 {
			return err
		}
		if _, decodeErr := hex.DecodeString(args[2]); decodeErr != nil {
			return err
		}
		id = args[2]
	}
	path := "/v1/agents/" + url.PathEscape(id)
	if args[1] == "shutdown" {
		if _, err := call(ctx, http.MethodPost, path+"/shutdown", nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "Shutdown acknowledged by %s (agent ID %s).\n", consoleAgentName(a), a.ID)
		return nil
	}
	data, err := call(ctx, http.MethodGet, path+"/events", nil)
	if err != nil {
		return err
	}
	var events []struct {
		At                time.Time `json:"at"`
		Kind              string    `json:"kind"`
		Transport         string    `json:"transport"`
		DurationSeconds   int64     `json:"duration_seconds"`
		ReconnectAttempts uint32    `json:"reconnect_attempts"`
	}
	if err := json.Unmarshal(data, &events); err != nil {
		return err
	}
	fmt.Fprintf(out, "Lifecycle events for agent ID %s\n", id)
	if len(events) == 0 {
		fmt.Fprintln(out, "No recent lifecycle events.")
		return nil
	}
	for _, e := range events {
		fmt.Fprintf(out, "%s  %-23s %s", e.At.Local().Format("2006-01-02 15:04:05"), e.Kind, e.Transport)
		if e.DurationSeconds > 0 {
			fmt.Fprintf(out, "  duration=%s", (time.Duration(e.DurationSeconds) * time.Second).String())
		}
		if e.ReconnectAttempts > 0 {
			fmt.Fprintf(out, "  attempts=%d", e.ReconnectAttempts)
		}
		fmt.Fprintln(out)
	}
	return nil
}

func emptyDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseProfileOptions(args []string) (profileRequest, error) {
	var req profileRequest
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return req, fmt.Errorf("profile option %q must be key=value", arg)
		}
		v := value
		switch key {
		case "server":
			req.Server = &v
		case "transport":
			req.Transport = &v
		case "domain":
			req.Domain = &v
		case "fingerprint":
			req.Fingerprint = &v
		case "auth":
			req.AuthMode = &v
		case "token":
			req.Token = &v
		case "password":
			req.Password = &v
		case "token-file", "password-file":
			b, err := os.ReadFile(value)
			if err != nil {
				return req, err
			}
			v = strings.TrimSpace(string(b))
			if key == "token-file" {
				req.Token = &v
			} else {
				req.Password = &v
			}
		case "payload-profile":
			req.PayloadProfile = &v
		case "websocket-path":
			req.WebSocketPath = &v
		case "deployment-profile":
			profile, err := deployment.Load(value)
			if err != nil {
				return req, err
			}
			req.Deployment = &profile
		case "tls-server-name":
			req.TLSServerName = &v
		case "tls-insecure-skip-verify":
			if value != "true" && value != "false" {
				return req, errors.New("tls-insecure-skip-verify must be true or false")
			}
			b := value == "true"
			req.TLSInsecureSkipVerify = &b
		case "deny":
			req.DeniedCapabilities = &v
		case "routes":
			var routes []string
			if value != "" {
				routes = strings.Split(value, ",")
			}
			req.AdvertisedRoutes = &routes
		default:
			return req, fmt.Errorf("unknown profile option %q", key)
		}
	}
	return req, nil
}

func printDeployScript(out io.Writer, hosted hostedArtifactInfo, shell string) error {
	a := hosted.Artifact
	if hosted.TLSCertSHA256 != "" {
		if decoded, err := hex.DecodeString(hosted.TLSCertSHA256); err != nil || len(decoded) != sha256.Size {
			return errors.New("invalid payload host TLS certificate pin")
		}
	}
	if hosted.TLSPublicKeyPin != "" {
		if decoded, err := base64.StdEncoding.DecodeString(hosted.TLSPublicKeyPin); err != nil || len(decoded) != sha256.Size {
			return errors.New("invalid payload host TLS public key pin")
		}
	}
	switch shell {
	case "powershell":
		fmt.Fprintf(out, "param([string]$Destination = '%s')\n$ErrorActionPreference = 'Stop'\n$url = '%s'\n$expected = '%s'\n$Destination = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Destination)\n$temp = $Destination + '.download'\ntry {\n", psQuote(a.Filename), psQuote(hosted.Retrieval), a.SHA256)
		if hosted.TLSSelfSigned {
			fmt.Fprintf(out, "  $certPin = '%s'\n", hosted.TLSCertSHA256)
			fmt.Fprint(out, `  Add-Type -AssemblyName System.Net.Http
  if (-not ('ScopedArtifactTls' -as [type])) {
    $source = @'
using System;
using System.Net.Http;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
public static class ScopedArtifactTls {
    public static HttpClientHandler CreateHandler(string pin) {
        var handler = new HttpClientHandler();
        handler.ServerCertificateCustomValidationCallback =
            (HttpRequestMessage request, X509Certificate2 certificate, X509Chain chain, SslPolicyErrors errors) => {
                if (string.IsNullOrEmpty(pin)) return true;
                if (certificate == null) return false;
                using (var sha = System.Security.Cryptography.SHA256.Create()) {
                    var actual = BitConverter.ToString(sha.ComputeHash(certificate.RawData)).Replace("-", "");
                    return string.Equals(actual, pin, StringComparison.OrdinalIgnoreCase);
                }
            };
        return handler;
    }
}
'@
    if ($PSVersionTable.PSVersion.Major -lt 6) {
      Add-Type -ReferencedAssemblies System.Net.Http -TypeDefinition $source
    } else {
      Add-Type -TypeDefinition $source
    }
  }
  $handler = [ScopedArtifactTls]::CreateHandler($certPin)
  $client = [System.Net.Http.HttpClient]::new($handler)
  try {
    $response = $client.GetAsync($url).GetAwaiter().GetResult()
    try {
      $response.EnsureSuccessStatusCode() | Out-Null
      $source = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      try {
        $target = [System.IO.File]::Create($temp)
        try { $source.CopyTo($target) } finally { $target.Dispose() }
      } finally { $source.Dispose() }
    } finally { $response.Dispose() }
  } finally { $client.Dispose(); $handler.Dispose() }
`)
		} else {
			fmt.Fprint(out, "  Invoke-WebRequest -Uri $url -OutFile $temp\n")
		}
		fmt.Fprint(out, "  $stream = [System.IO.File]::OpenRead($temp)\n  $sha256 = [System.Security.Cryptography.SHA256]::Create()\n  try { $actual = [System.BitConverter]::ToString($sha256.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }\n  finally { $sha256.Dispose(); $stream.Dispose() }\n  if ($actual -ne $expected) { throw 'Artifact SHA-256 mismatch' }\n  Move-Item -LiteralPath $temp -Destination $Destination -Force\n  Start-Process -FilePath $Destination -WindowStyle Hidden\n} finally { Remove-Item -LiteralPath $temp -ErrorAction SilentlyContinue }\n")
	case "shell":
		curlFlags := "-fL"
		if hosted.TLSSelfSigned {
			curlFlags += " -k"
		}
		if hosted.TLSPublicKeyPin != "" {
			curlFlags += " --pinnedpubkey 'sha256//" + hosted.TLSPublicKeyPin + "'"
		}
		fmt.Fprintf(out, "#!/bin/sh\nset -eu\ndest=${1:-./%s}\ntemp=\"${dest}.download\"\ntrap 'rm -f \"$temp\"' EXIT\ncurl %s '%s' -o \"$temp\"\nactual=$(sha256sum \"$temp\" | cut -d ' ' -f 1)\n[ \"$actual\" = '%s' ] || { echo 'Artifact SHA-256 mismatch' >&2; exit 1; }\nmv -f \"$temp\" \"$dest\"\nchmod 700 \"$dest\"\nnohup \"$dest\" </dev/null >/dev/null 2>&1 &\n", a.Filename, curlFlags, shQuote(hosted.Retrieval), a.SHA256)
	default:
		return errors.New("deployment script must be powershell or shell")
	}
	return nil
}
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
func shQuote(s string) string { return strings.ReplaceAll(s, "'", "'\"'\"'") }
