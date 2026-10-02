package agentprofile

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agent"
	"undertow/internal/security"
)

func testProfile() Profile {
	return Profile{ID: "profile-1", Name: "office", Created: time.Now().UTC(), UndertowVersion: "test",
		Config: agent.Config{Version: agent.ConfigVersion, Server: "127.0.0.1:443", Transport: "websocket", Fingerprint: strings.Repeat("a", 64), AuthMode: "none", Credential: make([]byte, 32)}}
}

func TestStampedProfileValidation(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template")
	if err := os.WriteFile(template, []byte("MZ-template"), 0600); err != nil {
		t.Fatal(err)
	}
	e := Embedded{ProfileID: testProfile().ID, ArtifactID: "artifact-1", Config: testProfile().Config}
	path := filepath.Join(dir, "artifact.exe")
	hash, size, err := Stamp(template, path, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path, hash); err != nil {
		t.Fatal(err)
	}
	if size <= int64(len("MZ-template")) {
		t.Fatal("overlay missing")
	}
	read, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if read.ArtifactID != e.ArtifactID || read.Config.Server != e.Config.Server {
		t.Fatalf("wrong profile: %+v", read)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, management := range [][]byte{[]byte(`"name":"office"`), []byte(`"platform"`), []byte(`"architecture"`), []byte(`"undertow_version"`)} {
		if bytes.Contains(data, management) {
			t.Fatalf("management metadata embedded: %s", management)
		}
	}
	data[len(data)-3] = 3
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "unsupported embedded agent profile version 3") {
		t.Fatalf("expected format version error, got %v", err)
	}
	data[len(data)-3] = 2
	data[len(data)-45] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("expected integrity error, got %v", err)
	}
	copy(data[len(data)-8:], magic[:])
	binary.BigEndian.PutUint32(data[len(data)-12:len(data)-8], MaxProfileSize+1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("expected length error, got %v", err)
	}
}

func TestVersionAndOversize(t *testing.T) {
	p := testProfile()
	p.Config.Version = 3
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported embedded agent profile version 3") {
		t.Fatalf("wrong version error: %v", err)
	}
	p = testProfile()
	p.Config.Server = "0.0.0.0:443"
	if err := p.Validate(); err == nil {
		t.Fatal("wildcard server was accepted")
	}
	dir := t.TempDir()
	template := filepath.Join(dir, "template")
	_ = os.WriteFile(template, []byte("MZ-template"), 0600)
	e := Embedded{ProfileID: testProfile().ID, ArtifactID: "artifact-1", Config: testProfile().Config}
	e.Config.WebSocketPath = "/" + strings.Repeat("x", MaxProfileSize)
	if _, _, err := Stamp(template, filepath.Join(dir, "artifact"), e); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("wrong oversize error: %v", err)
	}
}

func TestReadLegacyVersionOneArtifact(t *testing.T) {
	legacy := struct {
		Profile         Profile   `json:"profile"`
		ArtifactID      string    `json:"artifact_id"`
		Platform        string    `json:"platform"`
		Architecture    string    `json:"architecture"`
		Created         time.Time `json:"created"`
		UndertowVersion string    `json:"undertow_version"`
	}{Profile: testProfile(), ArtifactID: "legacy-artifact", Platform: "windows", Architecture: "amd64", Created: time.Now().UTC(), UndertowVersion: "old"}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	file := append([]byte("MZ-template"), payload...)
	file = append(file, sum[:]...)
	file = append(file, length[:]...)
	file = append(file, legacyMagic[:]...)
	path := filepath.Join(t.TempDir(), "old.exe")
	if err := os.WriteFile(path, file, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got.ProfileID != legacy.Profile.ID || got.ArtifactID != legacy.ArtifactID || got.Config.Server != legacy.Profile.Config.Server {
		t.Fatalf("legacy read: %+v, %v", got, err)
	}
}

func TestStoreImmutableArtifactSnapshots(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	_ = os.MkdirAll(templates, 0700)
	if err := os.WriteFile(filepath.Join(templates, "undertow-agent-linux-amd64"), []byte("ELF-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplateManifest(templates, "test", []string{"undertow-agent-linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(filepath.Join(dir, "store"), templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create("office", testProfile().Config)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Build("office", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config
	cfg.Server = "127.0.0.2:443"
	if _, err := s.Edit("office", cfg); err != nil {
		t.Fatal(err)
	}
	b, err := s.Build("office", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.SHA256 == b.SHA256 {
		t.Fatal("artifacts are not distinct")
	}
	old, err := Read(filepath.Join(dir, "store", "artifacts", a.Filename))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := Read(filepath.Join(dir, "store", "artifacts", b.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if old.Config.Server != "127.0.0.1:443" || newer.Config.Server != "127.0.0.2:443" {
		t.Fatal("profile edit mutated an artifact")
	}
	if bytes.Equal(old.Config.Credential, p.Config.Credential) || bytes.Equal(old.Config.Credential, newer.Config.Credential) {
		t.Fatal("artifact credentials are not independent")
	}
	_, identity, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte("test transcript")
	oldProof := security.MakeAuth(old.Config.Credential, identity, transcript)
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, oldProof, transcript); err != nil || artifactID != a.ID {
		t.Fatalf("old enrollment: %s %v", artifactID, err)
	}
	newProof := security.MakeAuth(newer.Config.Credential, identity, transcript)
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, newProof, transcript); err != nil || artifactID != b.ID {
		t.Fatalf("new enrollment: %s %v", artifactID, err)
	}
	manualProof := security.MakeAuth(p.Config.Credential, identity, transcript)
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, manualProof, transcript); err != nil || artifactID != "" {
		t.Fatalf("manual enrollment: %s %v", artifactID, err)
	}
	if _, err := s.Revoke(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifyEnrollment(p.Config.Credential, oldProof, transcript); err == nil {
		t.Fatal("revoked artifact enrolled")
	}
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, newProof, transcript); err != nil || artifactID != b.ID {
		t.Fatal("revocation affected unrelated artifact")
	}
	if _, err := s.Host(a.ID[:8]); err != nil {
		t.Fatal(err)
	}
	token, err := s.HostedToken(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, file, err := s.OpenHosted(token); err != nil {
		t.Fatal(err)
	} else {
		file.Close()
	}
	if _, err := s.Unhost(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, file, err := s.OpenHosted(token); err == nil {
		file.Close()
		t.Fatal("unhosted artifact still available")
	}
	if _, err := s.Host(a.ID); err != nil {
		t.Fatal(err)
	}
	newToken, err := s.HostedToken(a.ID)
	if err != nil || newToken == token || len(newToken) != 48 {
		t.Fatal("rehost did not rotate high-entropy retrieval token")
	}
	if err := WriteTemplateManifest(templates, "other", []string{"undertow-agent-linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Build("office", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "templates are for Undertow other") {
		t.Fatalf("version mismatch was accepted: %v", err)
	}
	if err := WriteTemplateManifest(templates, "test", []string{"undertow-agent-linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "undertow-agent-linux-amd64"), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Build("office", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("tampered template was accepted: %v", err)
	}
}
