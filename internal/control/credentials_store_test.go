package control

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialVaultEncryptionAccessAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "unique-vault-test-password-9f7c"
	private, err := store.CreateCredential("alice", CredentialInput{Label: "Operator account", Domain: "LAB", Username: "alice", Kind: "password", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if private.Account() != `LAB\alice` {
		t.Fatalf("account=%q", private.Account())
	}
	var sealed []byte
	if err := store.db.QueryRow("SELECT ciphertext FROM credentials WHERE id=?", private.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("plaintext secret in database ciphertext")
	}
	encoded, _ := json.Marshal(private)
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("secret in credential metadata")
	}
	if err := store.RecordAudit(AuditRecord{ID: "credential-audit", Action: "POST credentials.use", Target: "/v1/deployments/example/start", OperatorID: "alice", CredentialID: private.ID}); err != nil {
		t.Fatal(err)
	}
	audit, err := store.AuditHistory(1)
	if err != nil || len(audit) != 1 || audit[0].CredentialID != private.ID || audit[0].OperatorID != "alice" {
		t.Fatalf("credential audit=%+v err=%v", audit, err)
	}
	auditJSON, _ := json.Marshal(audit)
	if bytes.Contains(auditJSON, []byte(secret)) {
		t.Fatal("secret in audit history")
	}
	if _, err := store.ResolveCredential(private.ID, "bob", OperatorRole); err == nil {
		t.Fatal("another operator read private credential")
	}
	resolved, err := store.ResolveCredential(private.ID, "alice", OperatorRole)
	if err != nil || resolved.Secret != secret {
		t.Fatalf("owner resolution: %v", err)
	}
	if _, err := store.ListCredentials("bob", OperatorRole); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resolved, err = store.ResolveCredential(private.ID, "alice", OperatorRole)
	if err != nil || resolved.Secret != secret {
		t.Fatalf("restart resolution: %v", err)
	}
	shared, err := store.CreateCredential("alice", CredentialInput{Label: "Hash", Username: "service", Domain: "LAB", Kind: "nt_hash", Secret: strings.Repeat("a", 32), Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListCredentials("bob", OperatorRole)
	if err != nil || len(items) != 1 || items[0].ID != shared.ID {
		t.Fatalf("shared list=%+v err=%v", items, err)
	}
	if err := store.DeleteCredential(shared.ID, "bob", OperatorRole); err == nil {
		t.Fatal("non-owner removed shared credential")
	}
	if _, err := store.ReplaceCredential(private.ID, "bob", OperatorRole, CredentialInput{Label: "Changed", Username: "alice", Kind: "password", Secret: "replacement"}); err == nil {
		t.Fatal("non-owner replaced credential")
	}
	if err := store.DeleteCredential(shared.ID, "leader", TeamLeaderRole); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCredential(shared.ID, "alice", OperatorRole); err == nil {
		t.Fatal("removed credential remained usable")
	}
}

func TestCredentialVaultMissingKeyFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredential("alice", CredentialInput{Label: "Test", Username: "alice", Kind: "password", Secret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".credentials.key"); err != nil {
		t.Fatal(err)
	}
	if reopened, err := OpenOperationsStore(path); err == nil {
		reopened.Close()
		t.Fatal("missing key silently regenerated")
	}
}
