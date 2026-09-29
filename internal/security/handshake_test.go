package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestHandshakePinAuthAndCookie(t *testing.T) {
	_, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{9}, 32)
	cookie, err := MakeCookie(secret, "127.0.0.1", client.Hello(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.AcceptCookie(cookie); err != nil {
		t.Fatal(err)
	}
	if !CheckCookie(secret, "127.0.0.1", client.Hello(), time.Now()) {
		t.Fatal("cookie rejected")
	}
	if CheckCookie(secret, "127.0.0.2", client.Hello(), time.Now()) {
		t.Fatal("cookie accepted for wrong source")
	}
	hello, srv, err := NewServerHello(serverKey, client.Hello(), 123)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.VerifyServerHello(hello, "bad"); err == nil {
		t.Fatal("accepted wrong pin")
	}
	ck, err := client.VerifyServerHello(hello, Fingerprint(serverKey))
	if err != nil {
		t.Fatal(err)
	}
	sk, err := srv.Keys(client.Hello())
	if err != nil {
		t.Fatal(err)
	}
	if ck != sk {
		t.Fatal("directional keys differ")
	}
	token := bytes.Repeat([]byte{7}, 32)
	auth := MakeAuth(token, agentKey, client.Transcript)
	if _, err = CheckAuth(token, auth, srv.Transcript); err != nil {
		t.Fatal(err)
	}
	auth[42] ^= 1
	if _, err = CheckAuth(token, auth, srv.Transcript); err == nil {
		t.Fatal("accepted modified signature")
	}
}
