package authcontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSealedLogonSingleUseExpiryAndMetadataBoundary(t *testing.T) {
	keys := &CreationKeys{}
	key, err := keys.Issue()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealLogon(key, LogonRequest{User: "fixture-user", Password: "fixture-secret", LogonType: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(sealed)
	if strings.Contains(string(data), "fixture-secret") || strings.Contains(string(data), "fixture-user") {
		t.Fatal("plaintext sent to server")
	}
	r, err := keys.Open(sealed)
	if err != nil || r.Password != "fixture-secret" || r.User != "fixture-user" {
		t.Fatal("agent did not recover logon request", err)
	}
	r.Password = ""
	if _, err = keys.Open(sealed); err == nil {
		t.Fatal("creation replay accepted")
	}
	key, err = keys.Issue()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err = SealLogon(key, LogonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	keys.mu.Lock()
	private := keys.keys[key.ID]
	private.expires = time.Now().Add(-time.Second)
	keys.keys[key.ID] = private
	keys.mu.Unlock()
	if _, err = keys.Open(sealed); err == nil {
		t.Fatal("expired agent key accepted")
	}
}
func TestSealedLogonRejectsTamperingAndForeignAgent(t *testing.T) {
	keys := &CreationKeys{}
	key, err := keys.Issue()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealLogon(key, LogonRequest{Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	other := &CreationKeys{}
	if _, err = other.Open(sealed); err == nil {
		t.Fatal("foreign agent accepted logon")
	}
	sealed.Ciphertext[0] ^= 0x80
	if _, err = keys.Open(sealed); err == nil {
		t.Fatal("tampered logon accepted")
	}
	if _, err = keys.Open(sealed); err == nil {
		t.Fatal("failed creation key reused")
	}
	key, err = keys.Issue()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err = SealLogon(key, LogonRequest{})
	if err != nil {
		t.Fatal(err)
	}
	keys.Clear()
	if _, err = keys.Open(sealed); err == nil {
		t.Fatal("clear did not invalidate creation material")
	}
}
