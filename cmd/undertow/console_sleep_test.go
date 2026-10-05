package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestConsoleSleepPolicyParity(t *testing.T) {
	var saved control.SleepPolicy
	call := func(_ context.Context, method, path string, body any) ([]byte, error) {
		switch method + " " + path {
		case "GET /v1/status":
			return []byte(`{"agents":[{"id":"agent-a","hostname":"workstation","sleep_supported":true,"sleep":{"interval_seconds":3,"jitter_percent":10}}]}`), nil
		case "PUT /v1/agents/agent-a/sleep":
			saved = body.(control.SleepPolicy)
			return []byte(`{"interval_seconds":7,"jitter_percent":20}`), nil
		}
		return nil, errors.New("unexpected request")
	}
	var out bytes.Buffer
	if err := runConsoleAgentDistribution(context.Background(), &out, call, []string{"agent", "sleep", "agent-a"}); err != nil || !strings.Contains(out.String(), "3 seconds, 10%") {
		t.Fatalf("show: %s %v", out.String(), err)
	}
	out.Reset()
	if err := runConsoleAgentDistribution(context.Background(), &out, call, []string{"agent", "sleep", "agent-a", "7", "20"}); err != nil || saved != (control.SleepPolicy{IntervalSeconds: 7, JitterPercent: 20}) {
		t.Fatalf("save: %s %+v %v", out.String(), saved, err)
	}
	profile, err := parseProfileOptions([]string{"sleep-seconds=12", "sleep-jitter=25"})
	if err != nil || profile.SleepSeconds == nil || *profile.SleepSeconds != 12 || profile.SleepJitter == nil || *profile.SleepJitter != 25 {
		t.Fatalf("profile sleep options: %+v %v", profile, err)
	}
	if _, err := parseProfileOptions([]string{"sleep-seconds=oops"}); err == nil {
		t.Fatal("accepted invalid profile sleep")
	}
}
