package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestSelectedAgentDeploymentConsoleUsesStructuredAPI(t *testing.T) {
	var paths []string
	call := func(_ context.Context, method, path string, body any) ([]byte, error) {
		paths = append(paths, method+" "+path)
		switch path {
		case "/v1/deployments":
			request, ok := body.(map[string]string)
			if !ok || request["source_agent_id"] != "source-agent" || request["target"] != "ws01" || request["artifact_id"] != "build-one" || request["method"] != "winrm" || request["context"] != "current-user" {
				t.Fatalf("deployment request: %#v", body)
			}
			return json.Marshal(control.DeploymentRecord{ID: "deployment-one", Target: "ws01", Method: "winrm"})
		case "/v1/deployments/deployment-one/prepare":
			return json.Marshal(control.DeploymentRecord{ID: "deployment-one", State: "prepared"})
		case "/v1/deployments/deployment-one/start":
			request, ok := body.(map[string]string)
			if !ok || request["delivery"] != "agent-channel" || request["install_path"] != `C:\Windows\Temp\agent.exe` {
				t.Fatalf("deployment start request: %#v", body)
			}
			return json.Marshal(control.DeploymentRecord{ID: "deployment-one", State: "waiting", JobID: "job-one"})
		case "/v1/deployments?source_agent_id=source-agent":
			return []byte(`[]`), nil
		default:
			t.Fatalf("unexpected API path %s", path)
			return nil, nil
		}
	}
	var output bytes.Buffer
	if err := runConsoleDeployment(context.Background(), &output, call, []string{"deploy", "create", "ws01", "build-one", "winrm", "current-user"}, "source-agent"); err != nil {
		t.Fatal(err)
	}
	if err := runConsoleDeployment(context.Background(), &output, call, []string{"deploy", "prepare", "deployment-one"}, "source-agent"); err != nil {
		t.Fatal(err)
	}
	if err := runConsoleDeployment(context.Background(), &output, call, []string{"deploy", "start", "deployment-one", `C:\Windows\Temp\agent.exe`}, "source-agent"); err != nil {
		t.Fatal(err)
	}
	if err := runConsoleDeployment(context.Background(), &output, call, []string{"deployments"}, "source-agent"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 || paths[0] != "POST /v1/deployments" || paths[1] != "POST /v1/deployments/deployment-one/prepare" || paths[2] != "POST /v1/deployments/deployment-one/start" || paths[3] != "GET /v1/deployments?source_agent_id=source-agent" || !strings.Contains(output.String(), "Job job-one") {
		t.Fatalf("paths=%v output=%s", paths, output.String())
	}
}
