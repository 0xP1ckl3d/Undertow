package dns

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"undertow/internal/security"
)

func TestTwoClientsAndReconnect(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{43}, 32)
	srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx) }()
	go func() {
		for {
			select {
			case p := <-srv.Accepted():
				if p == nil {
					return
				}
				go func() {
					for {
						b, err := p.Session.Recv(ctx)
						if err != nil {
							return
						}
						if err = p.Session.Send(ctx, b); err != nil {
							return
						}
					}
				}()
			case <-ctx.Done():
				return
			}
		}
	}()
	keys := make([]ed25519.PrivateKey, 2)
	clients := make([]*Client, 2)
	for i := range keys {
		_, keys[i], err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		clients[i], err = Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, keys[i])
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}
	if got := len(srv.Peers()); got != 2 {
		t.Fatalf("got %d authenticated clients", got)
	}
	for i, c := range clients {
		msg := bytes.Repeat([]byte{byte(i + 1)}, 2400)
		if err = c.Send(ctx, msg); err != nil {
			t.Fatal(err)
		}
		got, err := c.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatal("cross-session or corrupted echo")
		}
	}
	oldID := clients[0].Session.ID()
	clients[0].Close()
	reconnected, err := DialProfile(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, keys[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	if reconnected.Session.ID() == oldID {
		t.Fatal("reconnect reused session ID")
	}
	msg := bytes.Repeat([]byte("after reconnect"), 100)
	if err = reconnected.Send(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got, err := reconnected.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("reconnect echo mismatch")
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to stop")
	}
}

func TestPasswordAndOpenEnrollmentOverDNS(t *testing.T) {
	for _, mode := range []string{"password", "none"} {
		t.Run(mode, func(t *testing.T) {
			_, identity, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			_, clientKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := security.Fingerprint(identity)
			password := ""
			if mode == "password" {
				password = "a memorable passphrase"
			}
			secret, err := security.EnrollmentSecret(mode, "", "", password, "", fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, secret)
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = srv.Serve(ctx) }()
			found, err := DiscoverFingerprint(ctx, srv.Addr().String(), "t.undertow.invalid")
			if err != nil || found != fingerprint {
				t.Fatalf("discovered fingerprint=%q err=%v", found, err)
			}
			client, err := Dial(ctx, srv.Addr().String(), "t.undertow.invalid", found, secret, clientKey)
			if err != nil {
				t.Fatal(err)
			}
			client.Close()
			if mode == "password" {
				wrong := bytes.Repeat([]byte{0x9c}, 32)
				_, err = Dial(ctx, srv.Addr().String(), "t.undertow.invalid", found, wrong, clientKey)
				if err == nil || !strings.Contains(err.Error(), "enrollment rejected") {
					t.Fatalf("wrong password result: %v", err)
				}
			}
		})
	}
}

func TestEncryptedPacketsAtHelloLengths(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{42}, 32)
	srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	go func() {
		peer := <-srv.Accepted()
		for {
			message, err := peer.Session.Recv(ctx)
			if err != nil {
				return
			}
			if err := peer.Session.Send(ctx, message); err != nil {
				return
			}
		}
	}()
	client, err := DialProfile(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, clientKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, size := range []int{22, 24} {
		message := bytes.Repeat([]byte{byte(size)}, size)
		if err := client.Send(ctx, message); err != nil {
			t.Fatal(err)
		}
		got, err := client.Recv(ctx)
		if err != nil {
			t.Fatalf("%d-byte message: %v", size, err)
		}
		if !bytes.Equal(got, message) {
			t.Fatalf("%d-byte message was corrupted", size)
		}
	}
}
