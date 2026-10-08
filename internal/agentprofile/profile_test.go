package agentprofile

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agent"
	"undertow/internal/deployment"
	"undertow/internal/security"
)

func TestImportCustomArtifactCanBeHosted(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "store"), filepath.Join(dir, "templates"), "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("custom Windows executable bytes")
	digest := sha256.Sum256(payload)
	artifact, err := store.ImportCustom("Approved support tool", "windows", "amd64", "support-tool.exe", int64(len(payload)), hex.EncodeToString(digest[:]), true, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !artifact.Custom || artifact.Label != "Approved support tool" || artifact.Profile != artifact.Label || artifact.ProfileID != "" || artifact.UndertowVersion != "" || !artifact.ServiceCapable {
		t.Fatalf("custom artifact metadata: %+v", artifact)
	}
	if artifact.Filename != "support-tool.exe" || artifact.StoredFilename == artifact.Filename || filepath.Ext(artifact.StoredFilename) != ".exe" {
		t.Fatalf("custom artifact filenames: %+v", artifact)
	}
	stored, err := os.ReadFile(store.ArtifactPath(artifact))
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("stored bytes=%q err=%v", stored, err)
	}
	if _, err := store.Host(artifact.ID); err != nil {
		t.Fatal(err)
	}
	token, err := store.HostedToken(artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	hosted, file, err := store.OpenHosted(token)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	served, err := io.ReadAll(file)
	if err != nil || hosted.ID != artifact.ID || !bytes.Equal(served, payload) {
		t.Fatalf("hosted artifact=%+v bytes=%q err=%v", hosted, served, err)
	}
}

func TestImportCustomArtifactAllowsRepeatedOriginalFilename(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "store"), filepath.Join(dir, "templates"), "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("same named tool")
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	first, err := store.ImportCustom("First tool", "windows", "amd64", "tool.exe", int64(len(payload)), hash, false, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ImportCustom("Second tool", "windows", "amd64", "tool.exe", int64(len(payload)), hash, false, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if first.StoredFilename == second.StoredFilename || first.Filename != second.Filename {
		t.Fatalf("repeated filenames: first=%+v second=%+v", first, second)
	}
}

func TestImportCustomArtifactRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "store"), filepath.Join(dir, "templates"), "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("custom bytes")
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	tests := []struct {
		name, label, platform, arch, filename, hash string
		service                                     bool
	}{
		{name: "label", platform: "windows", arch: "amd64", filename: "tool.exe", hash: hash},
		{name: "path", label: "tool", platform: "windows", arch: "amd64", filename: `..\\tool.exe`, hash: hash},
		{name: "extension", label: "tool", platform: "windows", arch: "amd64", filename: "tool.bin", hash: hash},
		{name: "target", label: "tool", platform: "plan9", arch: "amd64", filename: "tool", hash: hash},
		{name: "service", label: "tool", platform: "linux", arch: "amd64", filename: "tool", hash: hash, service: true},
		{name: "hash", label: "tool", platform: "windows", arch: "amd64", filename: "tool.exe", hash: strings.Repeat("0", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.ImportCustom(test.label, test.platform, test.arch, test.filename, int64(len(payload)), test.hash, test.service, bytes.NewReader(payload))
			if err == nil {
				t.Fatal("invalid custom artifact was accepted")
			}
		})
	}
}

func testProfile() Profile {
	return Profile{ID: "profile-1", Name: "office", Created: time.Now().UTC(), UndertowVersion: "test",
		Config: agent.Config{Version: agent.ConfigVersion, Server: "127.0.0.1:443", Transport: "websocket", Fingerprint: strings.Repeat("a", 64), AuthMode: "none", Credential: make([]byte, 32)}}
}

func testEmbedded(t *testing.T) Embedded {
	t.Helper()
	cfg := testProfile().Config
	cfg.AuthMode = "artifact"
	return Embedded{ProfileID: testProfile().ID, ArtifactID: "artifact-1", Config: cfg}
}

func TestStampedProfileValidation(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template")
	if err := os.WriteFile(template, []byte("MZ-template"), 0600); err != nil {
		t.Fatal(err)
	}
	e := testEmbedded(t)
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
	if read.ArtifactID != e.ArtifactID || read.Config.Server != e.Config.Server || read.Config.AuthMode != "artifact" {
		t.Fatalf("wrong profile: %+v", read)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, management := range [][]byte{[]byte(`"name"`), []byte(`"platform"`), []byte(`"architecture"`), []byte(`"undertow_version"`), []byte(`"profile_id"`), []byte(`"artifact_id"`), []byte(`"identity_key"`), []byte(`"k":`), []byte(`"auth_mode"`)} {
		if bytes.Contains(data, management) {
			t.Fatalf("management metadata embedded: %s", management)
		}
	}
	data[len(data)-3] = 5
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "unsupported embedded format version 5") {
		t.Fatalf("expected format version error, got %v", err)
	}
	data[len(data)-3] = 4
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

func TestEmbeddedDeploymentRoundTrip(t *testing.T) {
	e := testEmbedded(t)
	e.Config.Deployment = deployment.Default()
	e.Config.Deployment.QUIC.ALPN = "field/2"
	e.Config.Deployment.WebSocket.Headers = map[string]string{"User-Agent": "FieldClient"}
	e.Config.Deployment.Reconnect.JitterPercent = 15
	data, err := encodeEmbedded(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeEmbedded(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Config.Deployment.QUIC.ALPN != "field/2" || decoded.Config.Deployment.WebSocket.Headers["User-Agent"] != "FieldClient" || decoded.Config.Deployment.Reconnect.JitterPercent != 15 {
		t.Fatalf("deployment settings lost from agent binary: %+v", decoded.Config.Deployment)
	}
}

func TestVersionAndOversize(t *testing.T) {
	p := testProfile()
	p.Config.Version = 3
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported configuration version 3") {
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
	e := testEmbedded(t)
	e.Config.WebSocketPath = "/" + strings.Repeat("x", MaxProfileSize)
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
	c, err := s.Build("office", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.SHA256 == b.SHA256 {
		t.Fatal("artifacts are not distinct")
	}
	if b.ID == c.ID || b.SHA256 == c.SHA256 {
		t.Fatal("rebuilding an unchanged profile reused an artifact identity")
	}
	old, err := Read(filepath.Join(dir, "store", "artifacts", a.Filename))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := Read(filepath.Join(dir, "store", "artifacts", b.Filename))
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := Read(filepath.Join(dir, "store", "artifacts", c.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if old.Config.Server != "127.0.0.1:443" || newer.Config.Server != "127.0.0.2:443" {
		t.Fatal("profile edit mutated an artifact")
	}
	if bytes.Equal(old.Config.Credential, p.Config.Credential) || bytes.Equal(old.Config.Credential, newer.Config.Credential) || bytes.Equal(newer.Config.Credential, rebuilt.Config.Credential) {
		t.Fatal("artifact credentials are not independent")
	}
	transcript := []byte("test transcript")
	_, firstKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, secondKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	oldProof := security.MakeAuth(old.Config.Credential, firstKey, transcript)
	firstID, artifactID, err := s.VerifyEnrollment(p.Config.Credential, oldProof, transcript)
	if err != nil || artifactID != a.ID {
		t.Fatalf("old enrollment: %s %v", artifactID, err)
	}
	secondCopyProof := security.MakeAuth(old.Config.Credential, secondKey, transcript)
	if secondID, artifactID, err := s.VerifyEnrollment(p.Config.Credential, secondCopyProof, transcript); err != nil || artifactID != a.ID || secondID == firstID {
		t.Fatalf("second copy enrollment: %s %v", artifactID, err)
	}
	newProof := security.MakeAuth(newer.Config.Credential, secondKey, transcript)
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, newProof, transcript); err != nil || artifactID != b.ID {
		t.Fatalf("new enrollment: %s %v", artifactID, err)
	}
	manualProof := security.MakeAuth(p.Config.Credential, firstKey, transcript)
	if _, artifactID, err := s.VerifyEnrollment(p.Config.Credential, manualProof, transcript); err != nil || artifactID != "" {
		t.Fatalf("manual enrollment: %s %v", artifactID, err)
	}
	if _, err := s.Revoke(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifyEnrollment(p.Config.Credential, oldProof, transcript); err == nil {
		t.Fatal("revoked artifact enrolled")
	}
	if _, _, err := s.VerifyEnrollment(p.Config.Credential, secondCopyProof, transcript); err == nil {
		t.Fatal("second copy of revoked artifact enrolled")
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

func TestNewWindowsArtifactsAdvertiseServiceSupport(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	name := "undertow-agent-windows-amd64.exe"
	if err := os.WriteFile(filepath.Join(templates, name), []byte("MZ-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplateManifest(templates, "test", []string{name}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, "store"), templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("office", testProfile().Config); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Build("office", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !artifact.ServiceCapable {
		t.Fatal("new Windows artifact lacks service capability metadata")
	}
}

func TestDeletingArtifactKeepsDeployedEnrollmentUntilRevoked(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	name := "undertow-agent-linux-amd64"
	if err := os.WriteFile(filepath.Join(templates, name), []byte("agent-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplateManifest(templates, "test", []string{name}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "store")
	store, err := OpenStore(root, templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile()
	if _, err := store.Create("office", profile.Config); err != nil {
		t.Fatal(err)
	}
	a, err := store.Build("office", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := Read(store.ArtifactPath(a))
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte("deleted-artifact-reconnect")
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	proof := security.MakeAuth(embedded.Config.Credential, key, transcript)
	if err := store.DeleteArtifact(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.Artifacts()) != 0 {
		t.Fatal("deleted artifact remains downloadable/listed")
	}
	if _, err := store.Artifact(a.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact remained available: %v", err)
	}
	store, err = OpenStore(root, templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, id, err := store.VerifyEnrollment(profile.Config.Credential, proof, transcript); err != nil || id != a.ID {
		t.Fatalf("deployed copy could not reconnect after deletion: %s %v", id, err)
	}
	if _, err := store.EnrollmentArtifact(a.ID); err != nil {
		t.Fatalf("enrollment metadata lost: %v", err)
	}
	if _, err := store.Revoke(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.VerifyEnrollment(profile.Config.Credential, proof, transcript); err == nil {
		t.Fatal("revoked deleted artifact enrolled")
	}
}
