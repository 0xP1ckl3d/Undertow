//go:build windows

package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

func TestReconnectInventoryFitsControlFrameWithTokenCapability(t *testing.T) {
	for _, denyTokens := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "denied"}[denyTokens], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, b := make(chan []byte, 256), make(chan []byte, 256)
			server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
			agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
			defer server.Close()
			defer agent.Close()
			identity := ArtifactIdentity{ProfileID: "17e6a39c452bdb2ea9f293be", ArtifactID: "80842abf093b5e48e18da3ea", ReconnectPolicy: "progressive", ReconnectAttempts: 4}
			caps := pivot.DefaultCapabilities()
			if denyTokens {
				caps.TokenContexts = false
			}
			if err := SendInventoryWithPolicy(ctx, agent, nil, caps, identity, SleepPolicy{IntervalSeconds: 15, JitterPercent: 20}); err != nil {
				t.Fatal(err)
			}
			first, err := server.RecvControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(first) > 700 {
				t.Fatalf("inventory is %d bytes, exceeding the control-frame budget", len(first))
			}
			var info AgentInfo
			if err := json.Unmarshal(first, &info); err != nil {
				t.Fatal(err)
			}
			info.Capabilities.ExpandInventory()
			if info.ArtifactID != identity.ArtifactID || info.Capabilities == nil || info.Capabilities.Allows("tokens") == denyTokens {
				t.Fatalf("reconnect inventory lost identity or token capability: %+v", info)
			}
		})
	}
}
