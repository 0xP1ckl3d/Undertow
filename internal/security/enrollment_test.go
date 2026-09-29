package security

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollmentModes(t *testing.T) {
	fingerprint := "f4b9783ee28f46e1fae4067c3caf4dc6e7a15379add7faf369dbf2e8eed5412e"
	passwordFile := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(passwordFile, []byte("a memorable passphrase\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := EnrollmentSecret("password", "", "", "", passwordFile, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	fromArgument, err := EnrollmentSecret("password", "", "", "a memorable passphrase", "", fingerprint)
	if err != nil || !bytes.Equal(fromFile, fromArgument) || len(fromFile) != 32 {
		t.Fatalf("password secret mismatch: %v", err)
	}
	other, err := EnrollmentSecret("password", "", "", "a memorable passphrase", "", "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil || bytes.Equal(fromFile, other) {
		t.Fatalf("password secret not bound to server identity: %v", err)
	}
	open, err := EnrollmentSecret("none", "missing", "", "", "", fingerprint)
	if err != nil || len(open) != 32 || bytes.Equal(open, fromFile) {
		t.Fatalf("open enrollment secret: %v", err)
	}
	if _, err := EnrollmentSecret("password", "", "", "short", "", fingerprint); err == nil {
		t.Fatal("accepted short password")
	}
	if _, err := EnrollmentSecret("none", "", "", "password", "", fingerprint); err == nil {
		t.Fatal("accepted password in open enrollment")
	}
}

func TestEnrollmentTokenFromArgument(t *testing.T) {
	hexToken := strings.Repeat("ab", 32)
	fromArgument, err := EnrollmentSecret("token", "", hexToken, "", "", "")
	if err != nil || len(fromArgument) != 32 {
		t.Fatalf("direct token: %v", err)
	}
	path := filepath.Join(t.TempDir(), "token.key")
	if err := os.WriteFile(path, []byte(hexToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := EnrollmentSecret("token", path, "", "", "", "")
	if err != nil || !bytes.Equal(fromArgument, fromFile) {
		t.Fatalf("direct and file tokens differ: %v", err)
	}
	if _, err := EnrollmentSecret("token", path, hexToken, "", "", ""); err == nil || !strings.Contains(err.Error(), "choose either") {
		t.Fatalf("accepted both token sources: %v", err)
	}
	if _, err := EnrollmentSecret("token", "", "not hex", "", "", ""); err == nil || strings.Contains(err.Error(), "not hex") {
		t.Fatalf("token validation exposed secret or accepted invalid input: %v", err)
	}
	if _, err := EnrollmentSecret("none", "", hexToken, "", "", ""); err == nil {
		t.Fatal("accepted token with open enrollment")
	}
}
