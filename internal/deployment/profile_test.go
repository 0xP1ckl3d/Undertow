package deployment

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentProfileOverlaysDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deployment.json")
	if err := os.WriteFile(path, []byte(`{"websocket":{"path":"/edge","headers":{"User-Agent":"Edge/1"}},"quic":{"alpn":"edge/2"},"reconnect":{"jitter_percent":20}}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.WebSocket.Path != "/edge" || p.WebSocket.Headers["User-Agent"] != "Edge/1" || p.QUIC.ALPN != "edge/2" || p.QUIC.KeepAlive.Value() != 15*time.Second || p.Reconnect.ManualDelay.Value() != 2*time.Second || p.Reconnect.JitterPercent != 20 {
		t.Fatalf("profile overlay changed defaults: %+v", p)
	}
}

func TestDeploymentProfileRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{"websocket":{"headers":{"Authorization":"secret"}}}`,
		`{"websocket":{"path":"/bad\r\nX: y"}}`,
		`{"quic":{"alpn":""}}`,
		`{"reconnect":{"jitter_percent":51}}`,
		`{"unknown":true}`,
	} {
		path := filepath.Join(t.TempDir(), "deployment.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted invalid profile %s", body)
		}
	}
}
