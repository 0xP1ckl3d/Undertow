//go:build windows

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/namedpipe"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/quic"
	"undertow/internal/transport/relay"
	"undertow/internal/transport/websocket"
)

// The same agent process must sleep and redial with its original identity on
// every carrier. The server-side live-condition checks have separate tests.
func TestIdleSleepReconnectsAcrossCarriers(t *testing.T) {
	for _, carrier := range []string{"dns", "quic", "websocket", "relay", "relay-smb"} {
		t.Run(carrier, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			_, serverKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			token := make([]byte, 32)
			if _, err := rand.Read(token); err != nil {
				t.Fatal(err)
			}
			accepted := make(chan transport.Peer, 4)
			config := Config{Version: ConfigVersion, Transport: carrier, Fingerprint: security.Fingerprint(serverKey), Credential: token, AuthMode: "none", Packaged: true, Sleep: control.SleepPolicy{IntervalSeconds: 1}}
			if carrier == "dns" || carrier == "quic" || carrier == "websocket" {
				var listener transport.Listener
				switch carrier {
				case "dns":
					config.Domain = "t.undertow.invalid"
					listener, err = dns.Listen("127.0.0.1:0", config.Domain, serverKey, token)
				case "quic":
					config.TLSInsecureSkipVerify = true
					listener, err = quic.Listen("127.0.0.1:0", "", "", true, serverKey, token)
				case "websocket":
					config.TLSInsecureSkipVerify = true
					config.WebSocketPath = "/undertow"
					listener, err = websocket.Listen("127.0.0.1:0", config.WebSocketPath, "", "", true, serverKey, token)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				config.Server = listener.Addr().String()
				go func() { _ = listener.Serve(ctx) }()
				go func() {
					for {
						peer, err := listener.Accept(ctx)
						if err != nil {
							return
						}
						accepted <- peer
					}
				}()
			} else {
				var listener net.Listener
				if carrier == "relay" {
					listener, err = net.Listen("tcp4", "127.0.0.1:0")
					if err == nil {
						config.Server = listener.Addr().String()
					}
				} else {
					name := fmt.Sprintf("undertow-sleep-%d", time.Now().UnixNano())
					listener, err = namedpipe.Listen(`\\.\pipe\` + name)
					config.Server = `\\127.0.0.1\pipe\` + name
				}
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						go func() {
							peer, err := relay.Accept(ctx, conn, "parent", serverKey, token)
							if err == nil {
								if carrier == "relay-smb" {
									peer.Carrier = "relay-smb"
								}
								accepted <- peer
							}
						}()
					}
				}()
			}
			if err := config.Validate(); err != nil {
				t.Fatal(err)
			}
			runDone := make(chan error, 1)
			go func() { runDone <- Run(ctx, config, nil) }()
			var firstID string
			var firstSession uint64
			for attempt := 0; attempt < 2; attempt++ {
				var peer transport.Peer
				select {
				case peer = <-accepted:
				case err := <-runDone:
					t.Fatalf("agent exited before callback: %v", err)
				case <-ctx.Done():
					t.Fatal("agent did not connect and callback")
				}
				id := peer.Snapshot().AgentID
				if attempt == 0 {
					firstID, firstSession = id, peer.Snapshot().ID
				} else if id != firstID || peer.Snapshot().ID == firstSession {
					t.Fatalf("callback changed identity or reused session: first=%s/%d next=%s/%d", firstID, firstSession, id, peer.Snapshot().ID)
				}
				serverMux := mux.New(ctx, peer.Channel(), true)
				go func() {
					for {
						data, err := serverMux.RecvControl(ctx)
						if err != nil {
							serverMux.Close()
							return
						}
						var message control.SleepMessage
						if json.Unmarshal(data, &message) != nil {
							continue
						}
						switch message.Kind {
						case "request":
							_ = serverMux.SendControl(ctx, control.EncodeSleepMessage("granted", nil))
						case "commit":
							_ = serverMux.SendControl(ctx, control.EncodeSleepMessage("committed", nil))
						case "sleeping":
							_ = serverMux.SendControl(ctx, control.EncodeSleepMessage("final", nil))
							time.AfterFunc(2*time.Second, func() { serverMux.Close() })
							return
						}
					}
				}()
			}
			cancel()
			select {
			case <-runDone:
			case <-time.After(5 * time.Second):
				t.Fatal("agent did not stop")
			}
		})
	}
}
