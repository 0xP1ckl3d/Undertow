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
	"sort"
	"strings"

	"undertow/internal/agentprofile"
)

func runConsoleAgentDistribution(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) < 2 {
		return errors.New("use agent profile|build|artifacts|host|hosted|unhost|delete|deploy-script")
	}
	switch args[1] {
	case "profile":
		if len(args) < 3 {
			return errors.New("use agent profile create|list|show|edit|delete")
		}
		switch args[2] {
		case "list":
			if len(args) != 3 {
				return errors.New("use agent profile list")
			}
			data, err := call(ctx, http.MethodGet, "/v1/agent-profiles", nil)
			if err != nil {
				return err
			}
			var profiles []publicAgentProfile
			if err := json.Unmarshal(data, &profiles); err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Fprintln(out, "No agent profiles.")
				return nil
			}
			for _, p := range profiles {
				fmt.Fprintf(out, "%-24s %-10s %-24s %s\n", p.Name, p.Transport, p.Server, p.ID[:min(8, len(p.ID))])
			}
			return nil
		case "show":
			if len(args) != 4 {
				return errors.New("use agent profile show NAME")
			}
			data, err := call(ctx, http.MethodGet, "/v1/agent-profiles/"+url.PathEscape(args[3]), nil)
			if err != nil {
				return err
			}
			var p publicAgentProfile
			if err := json.Unmarshal(data, &p); err != nil {
				return err
			}
			fmt.Fprintf(out, "Profile: %s\nID: %s\nCreated: %s\nUndertow: %s\nFormat: %d\nServer: %s\nTransport: %s\nFingerprint: %s\nAuthentication: %s\nCapabilities denied: %s\nRoutes: %s\n", p.Name, p.ID, p.Created.Format("2006-01-02 15:04 UTC"), p.UndertowVersion, p.FormatVersion, p.Server, p.Transport, p.Fingerprint, p.AuthMode, emptyDefault(p.DeniedCapabilities, "none"), emptyDefault(strings.Join(p.AdvertisedRoutes, ", "), "auto"))
			return nil
		case "create", "edit":
			if len(args) < 4 {
				return errors.New("use agent profile create|edit NAME [server=HOST:PORT transport=quic ...]")
			}
			req, err := parseProfileOptions(args[4:])
			if err != nil {
				return err
			}
			method := http.MethodPost
			path := "/v1/agent-profiles"
			if args[2] == "edit" {
				method = http.MethodPut
				path += "/" + url.PathEscape(args[3])
			} else {
				req.Name = args[3]
			}
			data, err := call(ctx, method, path, req)
			if err != nil {
				return err
			}
			var p publicAgentProfile
			if err := json.Unmarshal(data, &p); err != nil {
				return err
			}
			if method == http.MethodPost {
				fmt.Fprintf(out, "Created profile %s (%s).\n", p.Name, p.ID)
			} else {
				fmt.Fprintf(out, "Profile %s changed. Existing artifacts retain their original configuration. Build a new artifact to use the updated profile.\n", p.Name)
			}
			return nil
		case "delete":
			if len(args) != 4 {
				return errors.New("use agent profile delete NAME")
			}
			if _, err := call(ctx, http.MethodDelete, "/v1/agent-profiles/"+url.PathEscape(args[3]), nil); err != nil {
				return err
			}
			fmt.Fprintf(out, "Deleted profile %s. Existing artifacts remain.\n", args[3])
			return nil
		}
	case "build":
		if len(args) != 5 {
			return errors.New("use agent build PROFILE PLATFORM ARCH")
		}
		data, err := call(ctx, http.MethodPost, "/v1/agent-artifacts", map[string]string{"profile": args[2], "platform": args[3], "architecture": args[4]})
		if err != nil {
			return err
		}
		var a agentprofile.Artifact
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		fmt.Fprintf(out, "Created artifact %s: %s\nProfile: %s  Platform: %s/%s  SHA-256: %s\n", a.ID, a.Filename, a.Profile, a.Platform, a.Architecture, a.SHA256)
		return nil
	case "artifacts", "hosted":
		if len(args) != 2 {
			return errors.New("use agent artifacts or agent hosted")
		}
		data, err := call(ctx, http.MethodGet, "/v1/agent-artifacts", nil)
		if err != nil {
			return err
		}
		var artifacts []agentprofile.Artifact
		if err := json.Unmarshal(data, &artifacts); err != nil {
			return err
		}
		sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Created.After(artifacts[j].Created) })
		found := false
		for _, a := range artifacts {
			if args[1] == "hosted" && !a.Hosted {
				continue
			}
			found = true
			marker := ""
			if a.Hosted {
				marker = " hosted"
			}
			fmt.Fprintf(out, "%-10s %-22s %-15s %s%s\n", a.ID[:min(8, len(a.ID))], a.Profile, a.Platform+"/"+a.Architecture, a.Created.Format("2006-01-02 15:04 UTC"), marker)
		}
		if !found {
			fmt.Fprintln(out, "No agent artifacts.")
		}
		return nil
	case "host":
		if len(args) != 3 {
			return errors.New("use agent host ARTIFACT_ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/agent-artifacts/"+url.PathEscape(args[2])+"/host", nil)
		if err != nil {
			return err
		}
		var a hostedArtifactInfo
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		fmt.Fprintf(out, "Artifact hosted\nProfile: %s\nPlatform: %s/%s\nArtifact: %s\nFilename: %s\nSize: %d bytes\nSHA-256: %s\nRetrieval: %s\n", a.Profile, a.Platform, a.Architecture, a.ID, a.Filename, a.Size, a.SHA256, a.Retrieval)
		return nil
	case "unhost":
		if len(args) != 3 {
			return errors.New("use agent unhost ARTIFACT_ID")
		}
		if _, err := call(ctx, http.MethodDelete, "/v1/agent-artifacts/"+url.PathEscape(args[2])+"/host", nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Artifact unhosted.")
		return nil
	case "delete":
		if len(args) != 3 {
			return errors.New("use agent delete ARTIFACT_ID")
		}
		if _, err := call(ctx, http.MethodDelete, "/v1/agent-artifacts/"+url.PathEscape(args[2]), nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Artifact deleted. Its profile remains.")
		return nil
	case "deploy-script":
		if len(args) != 4 {
			return errors.New("use agent deploy-script ARTIFACT_ID powershell|shell")
		}
		data, err := call(ctx, http.MethodGet, "/v1/agent-artifacts/"+url.PathEscape(args[2]), nil)
		if err != nil {
			return err
		}
		var a agentprofile.Artifact
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		if !a.Hosted {
			return errors.New("host the artifact before generating a deployment script")
		}
		data, err = call(ctx, http.MethodGet, "/v1/agent-artifacts/"+url.PathEscape(a.ID)+"/host", nil)
		if err != nil {
			return err
		}
		var hosted hostedArtifactInfo
		if err := json.Unmarshal(data, &hosted); err != nil {
			return err
		}
		if !strings.HasPrefix(hosted.Retrieval, "https://") {
			return errors.New("no WebSocket HTTPS listener is available for retrieval")
		}
		return printDeployScript(out, hosted, args[3])
	}
	return errors.New("use agent profile|build|artifacts|host|hosted|unhost|delete|deploy-script")
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
	switch shell {
	case "powershell":
		fmt.Fprintf(out, "param([string]$Destination = '%s')\n$ErrorActionPreference = 'Stop'\n$url = '%s'\n$expected = '%s'\n$temp = $Destination + '.download'\ntry {\n", psQuote(a.Filename), psQuote(hosted.Retrieval), a.SHA256)
		if hosted.TLSSelfSigned {
			fmt.Fprint(out, "  if ($PSVersionTable.PSVersion.Major -ge 7) {\n    Invoke-WebRequest -Uri $url -OutFile $temp -SkipCertificateCheck\n  } else {\n    [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }\n    Invoke-WebRequest -Uri $url -OutFile $temp\n  }\n")
		} else {
			fmt.Fprint(out, "  Invoke-WebRequest -Uri $url -OutFile $temp\n")
		}
		fmt.Fprint(out, "  $actual = (Get-FileHash -Algorithm SHA256 -Path $temp).Hash.ToLowerInvariant()\n  if ($actual -ne $expected) { throw 'Artifact SHA-256 mismatch' }\n  Move-Item -Force $temp $Destination\n  Start-Process -FilePath (Resolve-Path $Destination)\n} finally { Remove-Item -ErrorAction SilentlyContinue $temp }\n")
	case "shell":
		curlFlags := "-fL"
		if hosted.TLSSelfSigned {
			curlFlags += " -k"
		}
		fmt.Fprintf(out, "#!/bin/sh\nset -eu\ndest=${1:-./%s}\ntemp=\"${dest}.download\"\ntrap 'rm -f \"$temp\"' EXIT\ncurl %s '%s' -o \"$temp\"\nactual=$(sha256sum \"$temp\" | cut -d ' ' -f 1)\n[ \"$actual\" = '%s' ] || { echo 'Artifact SHA-256 mismatch' >&2; exit 1; }\nmv -f \"$temp\" \"$dest\"\nchmod 700 \"$dest\"\nnohup \"$dest\" </dev/null >/dev/null 2>&1 &\n", a.Filename, curlFlags, shQuote(hosted.Retrieval), a.SHA256)
	default:
		return errors.New("deployment script must be powershell or shell")
	}
	return nil
}
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
func shQuote(s string) string { return strings.ReplaceAll(s, "'", "'\"'\"'") }
