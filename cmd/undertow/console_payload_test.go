package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"undertow/internal/agentprofile"
)

func TestPayloadConsoleWorkflowAndIdentifierGuidance(t *testing.T) {
	id := strings.Repeat("a", 24)
	agentID := strings.Repeat("b", 32)
	info := artifactInfo{Artifact: agentprofile.Artifact{ID: id, AgentID: agentID, Profile: "office", Platform: "windows", Architecture: "amd64", Filename: id + ".exe", SHA256: strings.Repeat("c", 64), Hosted: true}, ServerPath: `/srv/builds/` + id + `.exe`}
	hosted := hostedArtifactInfo{Artifact: info.Artifact, ServerPath: info.ServerPath, Retrieval: "https://example.com/download/opaque-token", RetrievalPath: "/download/opaque-token"}
	encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
	publicPath := "/download/"
	call := func(_ context.Context, method, path string, body any) ([]byte, error) {
		switch method + " " + path {
		case "GET /v1/payload-retrieval-path":
			return encode(map[string]string{"path": publicPath}), nil
		case "PUT /v1/payload-retrieval-path":
			changed := publicPath != body.(map[string]string)["path"]
			publicPath = body.(map[string]string)["path"]
			return encode(struct {
				Path    string `json:"path"`
				Changed bool   `json:"changed"`
			}{Path: publicPath, Changed: changed}), nil
		case "GET /v1/agent-profiles":
			return encode([]publicAgentProfile{{Name: "office"}}), nil
		case "GET /v1/agent-artifacts":
			return encode([]artifactInfo{info}), nil
		case "POST /v1/agent-artifacts":
			return encode(info), nil
		case "GET /v1/agent-artifacts/" + id + "/host", "POST /v1/agent-artifacts/" + id + "/host":
			return encode(hosted), nil
		default:
			return nil, errors.New("unexpected request: " + method + " " + path)
		}
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"payload"}, []string{"profile name", "PAYLOAD_ID", "agent ID", "server file path", "download URL"}},
		{[]string{"payload", "build", "office", "windows", "amd64"}, []string{"Payload ID: " + id, "Embedded agent ID: " + agentID, "Server file: " + info.ServerPath, "Next: payload host " + id}},
		{[]string{"payload", "list"}, []string{id, "office", "hosted"}},
		{[]string{"payload", "show", id[:8]}, []string{"Payload ID: " + id, "Embedded agent ID: " + agentID, "Server file: " + info.ServerPath, "Download URL: " + hosted.Retrieval, "Download path: " + hosted.RetrievalPath}},
		{[]string{"payload", "url", id}, []string{"Download URL: " + hosted.Retrieval, "Download path: " + hosted.RetrievalPath, "SHA-256:"}},
		{[]string{"payload", "hosted"}, []string{id, hosted.Retrieval, hosted.RetrievalPath}},
		{[]string{"agent", "host", id}, []string{"Payload ID: " + id, hosted.Retrieval}},
	} {
		var out bytes.Buffer
		var err error
		if tc.args[0] == "agent" {
			err = runConsoleAgentDistribution(context.Background(), &out, call, tc.args)
		} else {
			err = runConsolePayload(context.Background(), &out, call, tc.args)
		}
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v missing %q in %q", tc.args, want, out.String())
			}
		}
	}
	for _, tc := range []struct {
		ref, want string
	}{
		{"office", "profile name"},
		{agentID, "connected agent ID"},
		{"https://example.com/download/opaque-token", "download URL or file path"},
		{"missing", "payload list"},
	} {
		_, err := resolvePayload(context.Background(), call, tc.ref)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("reference %q: expected %q guidance, got %v", tc.ref, tc.want, err)
		}
	}
	var out bytes.Buffer
	if err := runConsoleAgentDistribution(context.Background(), &out, call, []string{"agent", "events", id}); err == nil || !strings.Contains(err.Error(), "payload show") {
		t.Fatalf("connected-agent command accepted payload ID: %v", err)
	}
	out.Reset()
	if err := runConsolePayload(context.Background(), &out, call, []string{"payload", "retrieval-path"}); err != nil || !strings.Contains(out.String(), "/download/") {
		t.Fatalf("retrieval path view: %v %s", err, out.String())
	}
	if err := runConsolePayload(context.Background(), &out, call, []string{"payload", "retrieval-path", "set", "/broken"}); err == nil || publicPath != "/download/" {
		t.Fatalf("invalid retrieval path was accepted: %v", err)
	}
	out.Reset()
	if err := runConsolePayload(context.Background(), &out, call, []string{"payload", "retrieval-path", "set", "/new/"}); err != nil || publicPath != "/new/" || !strings.Contains(out.String(), "earlier URLs no longer work") {
		t.Fatalf("retrieval path change: %v %s", err, out.String())
	}
}
