package dns

import (
	"testing"
	"time"

	"undertow/internal/session"
)

func TestPollTarget(t *testing.T) {
	cases := []struct {
		name string
		stats session.Stats
		health int
		want int
	}{
		{"idle", session.Stats{CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 1},
		{"initial backlog", session.Stats{Queued: 100, CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 16},
		{"healthy backlog", session.Stats{Queued: 100, CongestionWindow: 64, PeerReceiveWindow: 64, RTT: 100 * time.Millisecond}, 64, 64},
		{"loss backoff", session.Stats{Queued: 100, CongestionWindow: 32, PeerReceiveWindow: 64}, 4, 4},
		{"receive window", session.Stats{Queued: 100, InFlight: 2, CongestionWindow: 64, PeerReceiveWindow: 3}, 64, 5},
		{"low RTT", session.Stats{Queued: 100, CongestionWindow: 64, PeerReceiveWindow: 64, RTT: 5 * time.Millisecond}, 64, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pollTarget(tc.stats, tc.health); got != tc.want {
				t.Fatalf("target=%d want=%d", got, tc.want)
			}
		})
	}
}
