package agent

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"
	"undertow/internal/deployment"

	"undertow/internal/security"
)

func TestPackagedIdentityIsUniquePerProcess(t *testing.T) {
	first, err := loadIdentity(Config{Packaged: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadIdentity(Config{Packaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != ed25519.PrivateKeySize || len(second) != ed25519.PrivateKeySize || security.Fingerprint(first) == security.Fingerprint(second) {
		t.Fatal("two packaged processes received the same agent identity")
	}
}

func TestConfiguredReconnectScheduleAndJitter(t *testing.T) {
	p := deployment.Default()
	p.Reconnect.ManualDelay = deployment.Duration(7 * time.Second)
	p.Reconnect.ProgressiveDelays = []deployment.Duration{deployment.Duration(3 * time.Second), deployment.Duration(9 * time.Second)}
	p.Reconnect.HealthyAfter = deployment.Duration(time.Minute)
	p.Reconnect.JitterPercent = 20
	if got := reconnectDelayWithProfile(false, 4, p); got != 7*time.Second {
		t.Fatalf("manual callback: %s", got)
	}
	if got := reconnectDelayWithProfile(true, 4, p); got != 9*time.Second {
		t.Fatalf("packaged callback: %s", got)
	}
	if got := failureIndexAfterSessionWithProfile(2, 40*time.Second, true, p); got != 2 {
		t.Fatalf("early reset: %d", got)
	}
	for i := 0; i < 100; i++ {
		got := deployment.Jitter(10*time.Second, p.Reconnect.JitterPercent)
		if got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("jitter outside configured range: %s", got)
		}
	}
}

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
