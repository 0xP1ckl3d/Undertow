package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Payloads are built deployment binaries. Connected agents have a separate
// command namespace, even though older consoles used "agent" for both.
func isPayloadSubcommand(value string) bool {
	switch value {
	case "profile", "profiles", "build", "list", "artifacts", "show", "host", "hosted", "url", "download", "unhost", "revoke", "delete", "deploy-script", "retrieval-host", "host-agent", "agent-hosts", "unhost-agent", "verify-script-agent", "deploy-script-agent":
		return true
	}
	return false
}

func printPayloadWorkflow(out io.Writer) {
	fmt.Fprint(out, `Payload deployment
  1. payload profile create NAME server=HOST:PORT transport=quic
     Save reusable connection settings. Use "payload profiles" to find names.
  2. payload build NAME windows amd64
     Create a new binary with its own enrollment credential. Each running
     copy creates an independent agent identity when it starts.
  3. payload host PAYLOAD_ID
     Enable HTTPS download and print its opaque URL and path.
  4. Download, verify SHA-256, and run the binary without arguments.

Inspect: payload list | payload show PAYLOAD_ID | payload hosted | payload url PAYLOAD_ID
Save:    payload download PAYLOAD_ID [OUTPUT]
         Defaults to outputs/downloads/payloads/FILENAME here.
Manage:  payload unhost PAYLOAD_ID | payload revoke PAYLOAD_ID | payload delete PAYLOAD_ID
Helper:  payload deploy-script PAYLOAD_ID powershell|shell
Agent:   payload host-agent PAYLOAD_ID AGENT_ID BIND PUBLIC_HOST
         payload agent-hosts [PAYLOAD_ID] | payload unhost-agent HOST_ID
         payload verify-script-agent HOST_ID powershell|shell
         payload deploy-script-agent HOST_ID powershell|shell
Profile: payload profile show|edit|delete NAME

NAME is a profile name. PAYLOAD_ID is the 24-character build ID shown by
"payload build" and "payload list". A connected agent has a different,
32-character agent ID assigned when the payload process starts; use "agents"
and "agent events|shutdown" for it. Copies of one payload get separate IDs.
The server file path is where Undertow stores the build. The download URL is
for the endpoint; choose the endpoint's install path when downloading.
Path: payload retrieval-path | payload retrieval-path set /downloads/
Host: payload retrieval-host | payload retrieval-host set SERVER_HOST
Set the public HTTPS host separately when agents connect through a relay or
another address that endpoints cannot use to download the payload.
The public prefix is saved on the server. Changing it rotates download tokens,
permanently invalidating old URLs;
use "payload hosted" to retrieve the current URLs. A startup
--payload-retrieval-path flag overrides the saved prefix.
Hosting, enrollment revocation, and stopping a running agent are separate.
Connected VPN clients can manage and download payloads directly.
`)
}

func runConsolePayload(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) == 1 || len(args) == 2 && args[1] == "help" {
		printPayloadWorkflow(out)
		return nil
	}
	switch args[1] {
	case "host-agent", "agent-hosts", "unhost-agent", "verify-script-agent", "deploy-script-agent":
		return runPayloadAgentHost(ctx, out, call, args)
	case "retrieval-host":
		return runPayloadRetrievalHost(ctx, out, call, args)
	case "retrieval-path":
		return runPayloadRetrievalPath(ctx, out, call, args)
	case "profiles":
		if len(args) != 2 {
			return errors.New("use payload profiles to list profile names; type payload help for the workflow")
		}
		return printPayloadProfiles(ctx, out, call)
	case "profile":
		return runPayloadProfile(ctx, out, call, args)
	case "build":
		return runPayloadBuild(ctx, out, call, args)
	case "list", "artifacts", "hosted":
		if len(args) != 2 {
			return errors.New("use payload list, payload hosted, or payload show PAYLOAD_ID")
		}
		return printPayloadList(ctx, out, call, args[1] == "hosted")
	case "show", "url", "host", "unhost", "revoke", "delete":
		if len(args) != 3 {
			return fmt.Errorf("use payload %s PAYLOAD_ID; run payload list to find an ID", args[1])
		}
		return runPayloadAction(ctx, out, call, args[1], args[2])
	case "download":
		if len(args) != 3 && len(args) != 4 {
			return errors.New("use payload download PAYLOAD_ID [OUTPUT]; default: outputs/downloads/payloads/FILENAME")
		}
		outputPath := ""
		if len(args) == 4 {
			outputPath = args[3]
		}
		return runPayloadDownload(ctx, out, call, args[2], outputPath)
	case "deploy-script":
		if len(args) != 4 || args[3] != "powershell" && args[3] != "shell" {
			return errors.New("use payload deploy-script PAYLOAD_ID powershell|shell; host the payload first")
		}
		a, err := resolvePayload(ctx, call, args[2])
		if err != nil {
			return err
		}
		if !a.Hosted {
			return fmt.Errorf("payload %s has no download URL; run payload host %s first", a.ID, a.ID)
		}
		hosted, err := fetchHostedPayload(ctx, call, a.ID)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(hosted.Retrieval, "https://") {
			return errors.New("no HTTPS download URL is available; start a WebSocket HTTPS listener on the server")
		}
		return printDeployScript(out, hosted, args[3])
	}
	return fmt.Errorf("unknown payload command %q; type payload help for the four-step workflow", args[1])
}

func runPayloadRetrievalHost(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) == 2 {
		data, err := call(ctx, http.MethodGet, "/v1/payload-retrieval-host", nil)
		if err != nil {
			return err
		}
		var current struct {
			Host string `json:"host"`
		}
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		if current.Host == "" {
			fmt.Fprintln(out, "Public payload download host: inferred from each payload's server address. Set with: payload retrieval-host set SERVER_HOST")
		} else {
			fmt.Fprintf(out, "Public payload download host: %s\n", current.Host)
		}
		return nil
	}
	if len(args) != 4 || args[2] != "set" || !validRetrievalHost(args[3]) || args[3] == "" {
		return errors.New("use payload retrieval-host set SERVER_HOST; give a DNS name or IP address without scheme or port")
	}
	data, err := call(ctx, http.MethodPut, "/v1/payload-retrieval-host", map[string]string{"host": args[3]})
	if err != nil {
		return fmt.Errorf("could not change public payload download host: %w", err)
	}
	var updated struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(data, &updated); err != nil {
		return err
	}
	fmt.Fprintf(out, "Public payload download host set to %s. Run payload hosted to see current URLs; download tokens are unchanged.\n", updated.Host)
	return nil
}

func runPayloadRetrievalPath(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) == 2 {
		data, err := call(ctx, http.MethodGet, "/v1/payload-retrieval-path", nil)
		if err != nil {
			return err
		}
		var current struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		fmt.Fprintf(out, "Public payload download prefix: %s\nUse payload hosted to list current URLs. Change with: payload retrieval-path set /downloads/\n", current.Path)
		return nil
	}
	if len(args) != 4 || args[2] != "set" {
		return errors.New("use payload retrieval-path to view the public prefix, or payload retrieval-path set /downloads/ to change it")
	}
	if !validRetrievalPath(args[3]) {
		return errors.New("public prefix must be / or an absolute path ending in / with letters, numbers, _ or - in each segment; example: /downloads/")
	}
	data, err := call(ctx, http.MethodPut, "/v1/payload-retrieval-path", map[string]string{"path": args[3]})
	if err != nil {
		return fmt.Errorf("could not change the public payload download prefix: %w", err)
	}
	var updated struct {
		Path    string `json:"path"`
		Changed bool   `json:"changed"`
	}
	if err := json.Unmarshal(data, &updated); err != nil {
		return err
	}
	if !updated.Changed {
		fmt.Fprintf(out, "Public payload download prefix is already %s. Hosted URLs are unchanged.\n", updated.Path)
		return nil
	}
	fmt.Fprintf(out, "Public payload download prefix set to %s. Hosted download tokens were rotated; earlier URLs no longer work. Run payload hosted or payload url PAYLOAD_ID to get current URLs.\nThe setting is saved for restarts unless an explicit --payload-retrieval-path startup flag overrides it.\n", updated.Path)
	return nil
}

func payloadProfiles(ctx context.Context, call consoleCaller) ([]publicAgentProfile, error) {
	data, err := call(ctx, http.MethodGet, "/v1/agent-profiles", nil)
	if err != nil {
		return nil, err
	}
	var profiles []publicAgentProfile
	err = json.Unmarshal(data, &profiles)
	return profiles, err
}

func printPayloadProfiles(ctx context.Context, out io.Writer, call consoleCaller) error {
	profiles, err := payloadProfiles(ctx, call)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Fprintln(out, "No profiles yet. Start with: payload profile create NAME server=HOST:PORT transport=quic")
		return nil
	}
	fmt.Fprintln(out, "Profile name (use this to build)  Transport  Server")
	for _, p := range profiles {
		fmt.Fprintf(out, "%-32s %-10s %s\n", p.Name, p.Transport, p.Server)
	}
	fmt.Fprintln(out, "Next: payload build PROFILE_NAME windows amd64  (or linux amd64|arm64)")
	return nil
}

func runPayloadProfile(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) < 3 {
		return errors.New("use payload profile create|list|show|edit|delete; type payload help for the workflow")
	}
	switch args[2] {
	case "list":
		if len(args) != 3 {
			return errors.New("use payload profiles or payload profile list")
		}
		return printPayloadProfiles(ctx, out, call)
	case "show":
		if len(args) != 4 {
			return errors.New("use payload profile show PROFILE_NAME; find names with payload profiles")
		}
		if len(args[3]) == 24 {
			if _, err := hex.DecodeString(args[3]); err == nil {
				return fmt.Errorf("%s looks like a payload ID; use payload show %s, or payload profiles to find a profile name", args[3], args[3])
			}
		}
		data, err := call(ctx, http.MethodGet, "/v1/agent-profiles/"+url.PathEscape(args[3]), nil)
		if err != nil {
			return fmt.Errorf("profile %q not found; use payload profiles to find its name: %w", args[3], err)
		}
		var p publicAgentProfile
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		fmt.Fprintf(out, "Profile name: %s (use this with payload build)\nProfile ID: %s (server metadata; not a payload ID)\nServer: %s\nTransport: %s\nServer fingerprint: %s\nAuthentication: %s\nDenied capabilities: %s\nAdvertised routes: %s\nCreated: %s\n", p.Name, p.ID, p.Server, p.Transport, p.Fingerprint, p.AuthMode, emptyDefault(p.DeniedCapabilities, "none"), emptyDefault(strings.Join(p.AdvertisedRoutes, ", "), "automatic"), p.Created.Format("2006-01-02 15:04 UTC"))
		fmt.Fprintf(out, "Next: payload build %s windows amd64  (or linux amd64|arm64)\n", p.Name)
		return nil
	case "create", "edit":
		if len(args) < 4 || args[2] == "edit" && len(args) < 5 {
			return fmt.Errorf("use payload profile %s PROFILE_NAME [server=HOST:PORT transport=quic ...]; type help payload for fields", args[2])
		}
		req, err := parseProfileOptions(args[4:])
		if err != nil {
			return err
		}
		method, path := http.MethodPost, "/v1/agent-profiles"
		if args[2] == "edit" {
			method, path = http.MethodPut, path+"/"+url.PathEscape(args[3])
		} else {
			req.Name = args[3]
		}
		data, err := call(ctx, method, path, req)
		if err != nil {
			return fmt.Errorf("could not %s profile %q; check server=HOST:PORT and type help payload for fields: %w", args[2], args[3], err)
		}
		var p publicAgentProfile
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		if args[2] == "edit" {
			fmt.Fprintf(out, "Profile %q updated. Existing payloads keep their original settings.\n", p.Name)
		} else {
			fmt.Fprintf(out, "Profile %q created.\n", p.Name)
		}
		fmt.Fprintf(out, "Next: payload build %s windows amd64  (or linux amd64|arm64)\n", p.Name)
		return nil
	case "delete":
		if len(args) != 4 {
			return errors.New("use payload profile delete PROFILE_NAME; find names with payload profiles")
		}
		if _, err := call(ctx, http.MethodDelete, "/v1/agent-profiles/"+url.PathEscape(args[3]), nil); err != nil {
			return fmt.Errorf("could not delete profile %q: %w", args[3], err)
		}
		fmt.Fprintf(out, "Profile %q deleted. Payloads already built from it remain.\n", args[3])
		return nil
	}
	return fmt.Errorf("unknown profile command %q; use payload profile create|list|show|edit|delete", args[2])
}

func runPayloadBuild(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	if len(args) != 5 && len(args) != 6 {
		return errors.New("use payload build PROFILE_NAME windows amd64 (or linux amd64|arm64) [filename=NAME]; find names with payload profiles")
	}
	target := args[3] + "/" + args[4]
	if target != "windows/amd64" && target != "linux/amd64" && target != "linux/arm64" {
		return fmt.Errorf("unsupported target %q; choose windows amd64, linux amd64, or linux arm64", target)
	}
	profiles, err := payloadProfiles(ctx, call)
	if err != nil {
		return err
	}
	found := false
	for _, p := range profiles {
		if p.Name == args[2] {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("profile name %q does not exist; run payload profiles (payload IDs belong to payload show/host)", args[2])
	}
	request := map[string]string{"profile": args[2], "platform": args[3], "architecture": args[4]}
	if len(args) == 6 {
		filename, ok := strings.CutPrefix(args[5], "filename=")
		if !ok || filename == "" {
			return errors.New("optional filename must be filename=NAME; for Windows use a .exe suffix")
		}
		request["filename"] = filename
	}
	data, err := call(ctx, http.MethodPost, "/v1/agent-artifacts", request)
	if err != nil {
		return fmt.Errorf("could not build payload from profile %q; check release templates on the server: %w", args[2], err)
	}
	var a artifactInfo
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	fmt.Fprintf(out, "Payload built\nPayload ID: %s (use this with payload host/show)\nProfile name: %s (settings snapshot)\nAgent ID: assigned when each process starts; see agents after connection\nTarget: %s/%s\nServer file: %s\nSHA-256: %s\nDownload: not hosted\nNext: payload host %s\n", a.ID, a.Profile, a.Platform, a.Architecture, a.ServerPath, a.SHA256, a.ID)
	return nil
}

func payloads(ctx context.Context, call consoleCaller) ([]artifactInfo, error) {
	data, err := call(ctx, http.MethodGet, "/v1/agent-artifacts", nil)
	if err != nil {
		return nil, err
	}
	var artifacts []artifactInfo
	err = json.Unmarshal(data, &artifacts)
	return artifacts, err
}

func resolvePayload(ctx context.Context, call consoleCaller, ref string) (artifactInfo, error) {
	artifacts, err := payloads(ctx, call)
	if err != nil {
		return artifactInfo{}, err
	}
	for _, a := range artifacts {
		if a.ID == ref || a.Filename == ref {
			return a, nil
		}
	}
	var matches []artifactInfo
	if len(ref) >= 4 {
		for _, a := range artifacts {
			if strings.HasPrefix(a.ID, ref) {
				matches = append(matches, a)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, a := range matches {
			ids = append(ids, a.ID)
		}
		sort.Strings(ids)
		return artifactInfo{}, fmt.Errorf("payload ID prefix %q matches multiple builds: %s; use the full ID from payload list", ref, strings.Join(ids, ", "))
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") || strings.ContainsAny(ref, `/\`) {
		return artifactInfo{}, errors.New("use a payload ID here, not a download URL or file path; run payload list to find the ID")
	}
	if len(ref) == 32 {
		if _, err := hex.DecodeString(ref); err == nil {
			return artifactInfo{}, fmt.Errorf("%s looks like a connected agent ID; use the 24-character payload ID from payload list", ref)
		}
	}
	profiles, err := payloadProfiles(ctx, call)
	if err == nil {
		for _, p := range profiles {
			if p.Name == ref {
				return artifactInfo{}, fmt.Errorf("%q is a profile name; first run payload build %s windows amd64 (or linux amd64|arm64), then use its payload ID", ref, ref)
			}
		}
	}
	return artifactInfo{}, fmt.Errorf("no payload matches %q; run payload list to see payload IDs", ref)
}

func fetchHostedPayload(ctx context.Context, call consoleCaller, id string) (hostedArtifactInfo, error) {
	data, err := call(ctx, http.MethodGet, "/v1/agent-artifacts/"+url.PathEscape(id)+"/host", nil)
	if err != nil {
		return hostedArtifactInfo{}, err
	}
	var hosted hostedArtifactInfo
	err = json.Unmarshal(data, &hosted)
	return hosted, err
}

func printPayloadList(ctx context.Context, out io.Writer, call consoleCaller, hostedOnly bool) error {
	artifacts, err := payloads(ctx, call)
	if err != nil {
		return err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Created.After(artifacts[j].Created) })
	count := 0
	if !hostedOnly {
		fmt.Fprintln(out, "Payload ID (use with payload show/host)  Profile name  Target  Download  Enrollment")
	}
	for _, a := range artifacts {
		if hostedOnly && !a.Hosted {
			continue
		}
		count++
		if hostedOnly {
			hosted, err := fetchHostedPayload(ctx, call, a.ID)
			if err != nil {
				return fmt.Errorf("could not retrieve URL for payload %s: %w", a.ID, err)
			}
			fmt.Fprintf(out, "Payload ID: %s  Profile: %s  Target: %s/%s\n", a.ID, a.Profile, a.Platform, a.Architecture)
			printPayloadDownload(out, hosted)
			if a.Revoked {
				fmt.Fprintln(out, "Enrollment: revoked; downloaded copies cannot start new sessions.")
			}
			fmt.Fprintln(out)
			continue
		}
		download, enrollment := "not hosted", "active"
		if a.Hosted {
			download = "hosted"
		}
		if a.Revoked {
			enrollment = "revoked"
		}
		fmt.Fprintf(out, "%-38s  %-20s  %-13s  %-10s  %s\n", a.ID, a.Profile, a.Platform+"/"+a.Architecture, download, enrollment)
	}
	if count == 0 {
		if hostedOnly {
			fmt.Fprintln(out, "No hosted payloads. Build one with payload build, then run payload host PAYLOAD_ID.")
		} else {
			fmt.Fprintln(out, "No payloads built yet. Start with payload profiles, then payload build PROFILE_NAME windows amd64.")
		}
	} else if !hostedOnly {
		fmt.Fprintln(out, "Use payload show PAYLOAD_ID for the server file and download URL; payload hosted lists all active URLs.")
	}
	return nil
}

func runPayloadAction(ctx context.Context, out io.Writer, call consoleCaller, action, ref string) error {
	a, err := resolvePayload(ctx, call, ref)
	if err != nil {
		return err
	}
	path := "/v1/agent-artifacts/" + url.PathEscape(a.ID)
	switch action {
	case "show", "url":
		var hosted *hostedArtifactInfo
		if a.Hosted {
			info, err := fetchHostedPayload(ctx, call, a.ID)
			if err != nil {
				return fmt.Errorf("could not retrieve hosted URL for payload %s: %w", a.ID, err)
			}
			hosted = &info
		}
		if action == "url" {
			if hosted == nil {
				return fmt.Errorf("payload %s has no download URL; run payload host %s first", a.ID, a.ID)
			}
			fmt.Fprintf(out, "Payload ID: %s\n", a.ID)
			printPayloadDownload(out, *hosted)
			fmt.Fprintf(out, "SHA-256: %s\n", a.SHA256)
			if a.Revoked {
				fmt.Fprintln(out, "Enrollment: revoked; downloaded copies cannot start new sessions.")
			}
			return nil
		}
		printPayloadDetails(out, a, hosted)
		return nil
	case "host":
		data, err := call(ctx, http.MethodPost, path+"/host", nil)
		if err != nil {
			return fmt.Errorf("could not host payload %s; the server needs an active WebSocket HTTPS listener: %w", a.ID, err)
		}
		var hosted hostedArtifactInfo
		if err := json.Unmarshal(data, &hosted); err != nil {
			return err
		}
		fmt.Fprintln(out, "Payload hosted for HTTPS download")
		printPayloadDetails(out, a, &hosted)
		fmt.Fprintf(out, "Next: download to the endpoint, verify SHA-256, and run without arguments. Reprint this URL with payload url %s.\n", a.ID)
		return nil
	case "unhost":
		if _, err := call(ctx, http.MethodDelete, path+"/host", nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "Payload %s is no longer downloadable. Existing copies and enrollment are unaffected.\n", a.ID)
	case "revoke":
		if _, err := call(ctx, http.MethodPost, path+"/revoke", nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "Payload %s enrollment revoked. Running agents stay connected; future connections using this build are rejected.\n", a.ID)
	case "delete":
		if _, err := call(ctx, http.MethodDelete, path, nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "Payload %s deleted from the server. Copies already on endpoints remain.\n", a.ID)
	}
	return nil
}

func printPayloadDetails(out io.Writer, a artifactInfo, hosted *hostedArtifactInfo) {
	agentIdentity := "assigned when each process starts; see agents after connection"
	if a.ProfileFormatVersion < 4 {
		agentIdentity = "legacy fixed identity; rebuild this payload before using copies on multiple hosts"
	}
	fmt.Fprintf(out, "Payload ID: %s (build record; use with payload commands)\nProfile name: %s (settings snapshot)\nAgent ID: %s\nTarget: %s/%s\nServer file: %s (stored on the Undertow server)\nSHA-256: %s\n", a.ID, a.Profile, agentIdentity, a.Platform, a.Architecture, a.ServerPath, a.SHA256)
	if hosted == nil {
		fmt.Fprintf(out, "Download: not hosted. Run payload host %s to enable an HTTPS URL.\n", a.ID)
	} else {
		printPayloadDownload(out, *hosted)
	}
	if a.Revoked {
		fmt.Fprintln(out, "Enrollment: revoked; downloaded copies cannot start new sessions.")
	} else {
		fmt.Fprintln(out, "Enrollment: active")
	}
}

func printPayloadDownload(out io.Writer, hosted hostedArtifactInfo) {
	if strings.HasPrefix(hosted.Retrieval, "https://") {
		fmt.Fprintf(out, "Download URL: %s\n", hosted.Retrieval)
	} else {
		fmt.Fprintln(out, "Download URL: unavailable; start the server's WebSocket HTTPS listener, then run payload url PAYLOAD_ID")
	}
	fmt.Fprintf(out, "Download path: %s (opaque public path; unrelated to the server file path)\n", hosted.RetrievalPath)
}
