package agent

import (
	"context"
	"testing"
	"time"
)

func TestProgressiveReconnectScheduleAndCancellation(t *testing.T) {
	want := []time.Duration{2, 5, 10, 30, 60, 120, 300, 300, 300}
	for i, seconds := range want {
		if got := reconnectDelay(true, i); got != seconds*time.Second {
			t.Fatalf("failure %d: got %s, want %ds", i, got, seconds)
		}
	}
	if reconnectDelay(false, 8) != 2*time.Second {
		t.Fatal("manual agent retry changed")
	}
	if failureIndexAfterSession(4, 100*time.Millisecond, true) != 4 {
		t.Fatal("brief connection reset backoff")
	}
	if failureIndexAfterSession(4, 31*time.Second, false) != 4 {
		t.Fatal("session without inventory reset backoff")
	}
	if failureIndexAfterSession(4, 31*time.Second, true) != 0 {
		t.Fatal("healthy session did not reset backoff")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if waitReconnect(ctx, 5*time.Minute) || time.Since(started) > time.Second {
		t.Fatal("cancellation did not interrupt reconnect wait")
	}
}
