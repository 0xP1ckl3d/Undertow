package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTokenPowerShellEncoding(t *testing.T) {
	want := bytes.Repeat([]byte{0x3b}, 32)
	value := hex.EncodeToString(want) + "\r\n"
	utf16 := []byte{0xff, 0xfe}
	for _, char := range []byte(value) {
		utf16 = append(utf16, char, 0)
	}
	path := filepath.Join(t.TempDir(), "token.key")
	if err := os.WriteFile(path, utf16, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadToken(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("UTF-16LE token rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+value), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadToken(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("UTF-8 BOM token rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil || !strings.Contains(err.Error(), "64 hexadecimal characters") {
		t.Fatalf("invalid token error = %v", err)
	}
}

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
