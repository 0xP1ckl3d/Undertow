package agentprofile

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agent"
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
	e := Embedded{Profile: testProfile(), ArtifactID: "artifact-1", Platform: "windows", Architecture: "amd64", Created: time.Now().UTC(), UndertowVersion: "test"}
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
	if read.ArtifactID != e.ArtifactID || read.Profile.Config.Server != e.Profile.Config.Server {
		t.Fatalf("wrong profile: %+v", read)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-2] = 3
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "unsupported embedded agent profile version 3") {
		t.Fatalf("expected format version error, got %v", err)
	}
	data[len(data)-2] = 1
	data[len(data)-53] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("expected integrity error, got %v", err)
	}
	copy(data[len(data)-16:], magic[:])
	binary.BigEndian.PutUint32(data[len(data)-20:len(data)-16], MaxProfileSize+1)
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
	e := Embedded{Profile: testProfile(), ArtifactID: "artifact-1", Platform: "windows", Architecture: "amd64", Created: time.Now().UTC(), UndertowVersion: strings.Repeat("x", MaxProfileSize)}
	if _, _, err := Stamp(template, filepath.Join(dir, "artifact"), e); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("wrong oversize error: %v", err)
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
	if old.Profile.Config.Server != "127.0.0.1:443" || newer.Profile.Config.Server != "127.0.0.2:443" {
		t.Fatal("profile edit mutated an artifact")
	}
	if !bytes.Equal(old.Profile.Config.Credential, p.Config.Credential) {
		t.Fatal("credential changed")
	}
	if _, err := s.Host(a.ID[:8]); err != nil {
		t.Fatal(err)
	}
	if _, file, err := s.OpenHosted(a.ID, a.Filename); err != nil {
		t.Fatal(err)
	} else {
		file.Close()
	}
	if _, err := s.Unhost(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, file, err := s.OpenHosted(a.ID, a.Filename); err == nil {
		file.Close()
		t.Fatal("unhosted artifact still available")
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
