package main

import (
	"testing"

	"undertow/internal/control"
)

func TestConsoleAgentTransitionIgnoresExpectedSleepCycle(t *testing.T) {
	connected := control.AgentInfo{ID: "agent", Online: true, ConnectionState: "connected"}
	sleeping := control.AgentInfo{ID: "agent", Offline: true, ConnectionState: "sleeping"}
	lost := control.AgentInfo{ID: "agent", Offline: true, ConnectionState: "disconnected"}
	for _, test := range []struct {
		name     string
		before   control.AgentInfo
		seen     bool
		after    control.AgentInfo
		expected string
	}{
		{"first connection", control.AgentInfo{}, false, connected, "connected"},
		{"enter sleep", connected, true, sleeping, ""},
		{"expected checkin", sleeping, true, connected, ""},
		{"sleep again", connected, true, sleeping, ""},
		{"missed checkins", sleeping, true, lost, "lost"},
		{"remain lost", lost, true, lost, ""},
		{"recover after loss", lost, true, connected, "connected"},
		{"continuous disconnect", connected, true, lost, "lost"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := consoleAgentTransition(test.before, test.seen, test.after); got != test.expected {
				t.Fatalf("notice = %q, want %q", got, test.expected)
			}
		})
	}
}
