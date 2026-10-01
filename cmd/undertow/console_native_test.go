package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestSelectedAgentStartsNativeJob(t *testing.T) {
	module, err := os.ReadFile(filepath.Join("..", "..", "examples", "native", "hello", "hello.module"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hello.module")
	dataPath := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, module, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte{0, 1, 255}, 0600); err != nil {
		t.Fatal(err)
	}
	seen := false
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a"}}})
		}
		if method == "POST" && route == "/v1/agents/agent-a/native/jobs" {
			encoded, _ := json.Marshal(body)
			var request struct {
				Source []byte   `json:"source"`
				Args   []string `json:"args"`
				Data   []byte   `json:"data"`
			}
			if json.Unmarshal(encoded, &request) != nil || !bytes.Equal(request.Source, module) || !reflect.DeepEqual(request.Args, []string{"two words"}) || !bytes.Equal(request.Data, []byte{0, 1, 255}) {
				t.Fatalf("native request=%s", encoded)
			}
			seen = true
			return json.Marshal(control.JobInfo{ID: "native-job", AgentID: "agent-a", Kind: "native"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var output bytes.Buffer
	input := "use 1\nrun-native --background --data \"" + dataPath + "\" \"" + path + "\" \"two words\"\nquit\n"
	if err := runConsole(context.Background(), strings.NewReader(input), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !seen || !strings.Contains(output.String(), "Native job native-job started") {
		t.Fatalf("request seen=%t output=%s", seen, output.String())
	}
}
