package agentprofile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"undertow/internal/agent"
	"undertow/internal/security"
)

type Artifact struct {
	ID                   string    `json:"id"`
	ProfileID            string    `json:"profile_id"`
	Profile              string    `json:"profile"`
	Server               string    `json:"server"`
	Platform             string    `json:"platform"`
	Architecture         string    `json:"architecture"`
	Filename             string    `json:"filename"`
	Size                 int64     `json:"size"`
	SHA256               string    `json:"sha256"`
	Created              time.Time `json:"created"`
	UndertowVersion      string    `json:"undertow_version"`
	ProfileFormatVersion uint32    `json:"profile_format_version"`
	Hosted               bool      `json:"hosted"`
	Revoked              bool      `json:"revoked,omitempty"`
}

type persisted struct {
	Profiles             map[string]Profile  `json:"profiles"`
	Artifacts            map[string]Artifact `json:"artifacts"`
	RetrievalTokens      map[string]string   `json:"retrieval_tokens,omitempty"`
	EnrollmentSecrets    map[string]string   `json:"enrollment_secrets,omitempty"`
	PayloadRetrievalPath string              `json:"payload_retrieval_path,omitempty"`
	PayloadRetrievalHost string              `json:"payload_retrieval_host,omitempty"`
}

const templateManifestName = "undertow-agent-templates.json"

type TemplateManifest struct {
	Version   string            `json:"version"`
	Templates map[string]string `json:"templates"`
}

// WriteTemplateManifest is used by release builds, never by a live server.
func WriteTemplateManifest(dir, version string, names []string) error {
	manifest := TemplateManifest{Version: version, Templates: make(map[string]string, len(names))}
	for _, name := range names {
		if !safeArtifactFilename(name) {
			return errors.New("invalid thin-agent template filename")
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		manifest.Templates[name] = hex.EncodeToString(h.Sum(nil))
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, templateManifestName), b, 0600)
}

func (s *Store) verifyTemplate(name, path string) error {
	b, err := os.ReadFile(filepath.Join(s.templates, templateManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("no verified thin-agent template manifest; build templates with the release script")
	}
	if err != nil {
		return err
	}
	var manifest TemplateManifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return fmt.Errorf("invalid thin-agent template manifest: %w", err)
	}
	if manifest.Version != s.version {
		return fmt.Errorf("thin-agent templates are for Undertow %s, server is %s", manifest.Version, s.version)
	}
	expected := manifest.Templates[name]
	if expected == "" {
		return fmt.Errorf("no verified %s thin-agent template is available", name)
	}
	if err := VerifyFile(path, expected); err != nil {
		return fmt.Errorf("thin-agent template %s: %w", name, err)
	}
	return nil
}

type Store struct {
	mu                       sync.Mutex
	root, templates, version string
	state                    persisted
}

func OpenStore(root, templates, version string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0700); err != nil {
		return nil, err
	}
	s := &Store{root: root, templates: templates, version: version, state: persisted{Profiles: map[string]Profile{}, Artifacts: map[string]Artifact{}, RetrievalTokens: map[string]string{}, EnrollmentSecrets: map[string]string{}}}
	b, err := os.ReadFile(filepath.Join(root, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Join(root, "state.json"), 0600); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("agent distribution store: %w", err)
	}
	if s.state.Profiles == nil {
		s.state.Profiles = map[string]Profile{}
	}
	if s.state.Artifacts == nil {
		s.state.Artifacts = map[string]Artifact{}
	}
	if s.state.RetrievalTokens == nil {
		s.state.RetrievalTokens = map[string]string{}
	}
	if s.state.EnrollmentSecrets == nil {
		s.state.EnrollmentSecrets = map[string]string{}
	}
	for id, a := range s.state.Artifacts {
		if id != a.ID || !safeArtifactFilename(a.Filename) {
			return nil, errors.New("invalid agent artifact record")
		}
		if a.Hosted && s.state.RetrievalTokens[id] == "" {
			// Legacy predictable URLs are not retained after the format upgrade.
			a.Hosted = false
			s.state.Artifacts[id] = a
		}
	}
	seenTokens := map[string]bool{}
	for id, token := range s.state.RetrievalTokens {
		if _, ok := s.state.Artifacts[id]; !ok || len(token) != 48 || seenTokens[token] {
			return nil, errors.New("invalid artifact retrieval token record")
		}
		if _, err := hex.DecodeString(token); err != nil {
			return nil, errors.New("invalid artifact retrieval token record")
		}
		seenTokens[token] = true
	}
	for id, encoded := range s.state.EnrollmentSecrets {
		if _, ok := s.state.Artifacts[id]; !ok || len(encoded) != 64 {
			return nil, errors.New("invalid artifact enrollment record")
		}
		if _, err := hex.DecodeString(encoded); err != nil {
			return nil, errors.New("invalid artifact enrollment record")
		}
	}
	return s, nil
}

func safeArtifactFilename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\:")
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.root, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.root, "state.json"))
}

func (s *Store) PayloadRetrievalPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.PayloadRetrievalPath
}

func (s *Store) SetPayloadRetrievalPath(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.state.PayloadRetrievalPath
	if old == path {
		return false, nil
	}
	oldTokens := s.state.RetrievalTokens
	if old != "" {
		newTokens := make(map[string]string, len(oldTokens))
		seen := make(map[string]bool, len(oldTokens))
		for id, token := range oldTokens {
			newTokens[id] = token
			seen[token] = true
		}
		for id, artifact := range s.state.Artifacts {
			if !artifact.Hosted {
				continue
			}
			for {
				first, err := ID()
				if err != nil {
					return false, err
				}
				second, err := ID()
				if err != nil {
					return false, err
				}
				token := first + second
				if !seen[token] {
					newTokens[id] = token
					seen[token] = true
					break
				}
			}
		}
		s.state.RetrievalTokens = newTokens
	}
	s.state.PayloadRetrievalPath = path
	if err := s.save(); err != nil {
		s.state.PayloadRetrievalPath = old
		s.state.RetrievalTokens = oldTokens
		return false, err
	}
	return true, nil
}

func (s *Store) PayloadRetrievalHost() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.PayloadRetrievalHost
}

func (s *Store) SetPayloadRetrievalHost(host string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.state.PayloadRetrievalHost
	if old == host {
		return false, nil
	}
	s.state.PayloadRetrievalHost = host
	if err := s.save(); err != nil {
		s.state.PayloadRetrievalHost = old
		return false, err
	}
	return true, nil
}

func (s *Store) Create(name string, cfg agent.Config) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Profiles[name]; ok {
		return Profile{}, errors.New("agent profile already exists")
	}
	id, err := ID()
	if err != nil {
		return Profile{}, err
	}
	p := Profile{ID: id, Name: name, Created: time.Now().UTC(), UndertowVersion: s.version, Config: cfg}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	s.state.Profiles[name] = p
	if err := s.save(); err != nil {
		delete(s.state.Profiles, name)
		return Profile{}, err
	}
	return p, nil
}

func (s *Store) Edit(name string, cfg agent.Config) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[name]
	if !ok {
		return Profile{}, os.ErrNotExist
	}
	old := p
	p.Config = cfg
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	s.state.Profiles[name] = p
	if err := s.save(); err != nil {
		s.state.Profiles[name] = old
		return Profile{}, err
	}
	return p, nil
}

func (s *Store) Profile(name string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[name]
	if !ok {
		return Profile{}, os.ErrNotExist
	}
	return p, nil
}
func (s *Store) Profiles() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Profile, 0, len(s.state.Profiles))
	for _, p := range s.state.Profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Store) DeleteProfile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[name]
	if !ok {
		return os.ErrNotExist
	}
	delete(s.state.Profiles, name)
	if err := s.save(); err != nil {
		s.state.Profiles[name] = p
		return err
	}
	return nil
}

func templateName(platform, arch string) (string, error) {
	switch platform + "/" + arch {
	case "windows/amd64", "linux/amd64", "linux/arm64":
	default:
		return "", fmt.Errorf("no %s %s thin-agent template is available", platform, arch)
	}
	name := "undertow-agent-" + platform + "-" + arch
	if platform == "windows" {
		name += ".exe"
	}
	return name, nil
}

func (s *Store) Build(name, platform, arch string, requestedFilename ...string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[name]
	if !ok {
		return Artifact{}, os.ErrNotExist
	}
	if p.Config.Transport == "relay-smb" && platform != "windows" {
		return Artifact{}, errors.New("SMB named-pipe relay payloads require a Windows target")
	}
	template, err := templateName(platform, arch)
	if err != nil {
		return Artifact{}, err
	}
	path := filepath.Join(s.templates, template)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, fmt.Errorf("no %s %s thin-agent template is available in %s", platform, arch, s.templates)
		}
		return Artifact{}, err
	}
	if err := s.verifyTemplate(template, path); err != nil {
		return Artifact{}, err
	}
	id, err := ID()
	if err != nil {
		return Artifact{}, err
	}
	filename := id
	if platform == "windows" {
		filename += ".exe"
	}
	if len(requestedFilename) > 0 && requestedFilename[0] != "" {
		filename = requestedFilename[0]
		if !safeArtifactFilename(filename) || len(filename) > 128 || strings.ContainsAny(filename, "\r\n") || (platform == "windows" && !strings.HasSuffix(strings.ToLower(filename), ".exe")) {
			return Artifact{}, errors.New("invalid artifact filename")
		}
	}
	a := Artifact{ID: id, ProfileID: p.ID, Profile: p.Name, Server: p.Config.Server, Platform: platform, Architecture: arch, Filename: filename, Created: time.Now().UTC(), UndertowVersion: s.version, ProfileFormatVersion: EmbeddedFormatVersion}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Artifact{}, err
	}
	configured := p.Config
	configured.AuthMode = "artifact"
	configured.Credential = secret
	e := Embedded{ProfileID: p.ID, ArtifactID: id, Config: configured}
	a.SHA256, a.Size, err = Stamp(path, filepath.Join(s.root, "artifacts", filename), e)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Artifact{}, fmt.Errorf("artifact filename %q already exists; choose a different filename", filename)
		}
		return Artifact{}, err
	}
	s.state.Artifacts[id] = a
	s.state.EnrollmentSecrets[id] = hex.EncodeToString(secret)
	if err := s.save(); err != nil {
		delete(s.state.Artifacts, id)
		delete(s.state.EnrollmentSecrets, id)
		os.Remove(filepath.Join(s.root, "artifacts", filename))
		return Artifact{}, err
	}
	return a, nil
}

func (s *Store) Artifacts() []Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Artifact, 0, len(s.state.Artifacts))
	for _, a := range s.state.Artifacts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}
func (s *Store) Artifact(id string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok {
		return Artifact{}, os.ErrNotExist
	}
	return a, nil
}

// ArtifactPath is the server-side file path, never an endpoint installation path.
func (s *Store) ArtifactPath(a Artifact) string {
	path := filepath.Join(s.root, "artifacts", a.Filename)
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func (s *Store) ArtifactEnrollmentScoped(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.EnrollmentSecrets[id] != ""
}
func (s *Store) find(id string) (Artifact, bool) {
	if a, ok := s.state.Artifacts[id]; ok {
		return a, true
	}
	var result Artifact
	found := false
	for _, a := range s.state.Artifacts {
		if strings.HasPrefix(a.ID, id) {
			if found {
				return Artifact{}, false
			}
			result, found = a, true
		}
	}
	return result, found
}
func (s *Store) setHosted(id string, hosted bool) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok {
		return Artifact{}, os.ErrNotExist
	}
	if hosted {
		if err := VerifyFile(filepath.Join(s.root, "artifacts", a.Filename), a.SHA256); err != nil {
			return Artifact{}, err
		}
	}
	old := a
	oldToken := s.state.RetrievalTokens[a.ID]
	a.Hosted = hosted
	if hosted && oldToken == "" {
		token, err := ID()
		if err != nil {
			return Artifact{}, err
		}
		token2, err := ID()
		if err != nil {
			return Artifact{}, err
		}
		s.state.RetrievalTokens[a.ID] = token + token2
	}
	if !hosted {
		delete(s.state.RetrievalTokens, a.ID)
	}
	s.state.Artifacts[a.ID] = a
	if err := s.save(); err != nil {
		s.state.Artifacts[a.ID] = old
		if oldToken != "" {
			s.state.RetrievalTokens[a.ID] = oldToken
		} else {
			delete(s.state.RetrievalTokens, a.ID)
		}
		return Artifact{}, err
	}
	return a, nil
}
func (s *Store) Host(id string) (Artifact, error)   { return s.setHosted(id, true) }
func (s *Store) Unhost(id string) (Artifact, error) { return s.setHosted(id, false) }

func (s *Store) Revoke(id string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok {
		return Artifact{}, os.ErrNotExist
	}
	if s.state.EnrollmentSecrets[a.ID] == "" {
		return Artifact{}, errors.New("legacy artifact has no dedicated credential; build a new artifact to use revocation")
	}
	old := a
	a.Revoked = true
	s.state.Artifacts[a.ID] = a
	if err := s.save(); err != nil {
		s.state.Artifacts[a.ID] = old
		return Artifact{}, err
	}
	return a, nil
}

// VerifyEnrollment accepts the existing manual/client credential and active
// artifact credentials. The artifact ID is propagated to the peer so an
// artifact credential cannot be used for a VPN client session.
func (s *Store) VerifyEnrollment(primary, auth, transcript []byte) ([16]byte, string, error) {
	id, err := security.VerifyAuthSignature(auth, transcript)
	if err != nil {
		return [16]byte{}, "", err
	}
	if security.CheckEnrollmentMAC(primary, auth, transcript) {
		return id, "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for artifactID, encoded := range s.state.EnrollmentSecrets {
		a := s.state.Artifacts[artifactID]
		if a.Revoked || encoded == "" {
			continue
		}
		secret, err := hex.DecodeString(encoded)
		if err != nil {
			continue
		}
		if security.CheckEnrollmentMAC(secret, auth, transcript) {
			return id, artifactID, nil
		}
	}
	return [16]byte{}, "", security.ErrHandshake
}

func (s *Store) HostedToken(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok || !a.Hosted || s.state.RetrievalTokens[a.ID] == "" {
		return "", os.ErrNotExist
	}
	return s.state.RetrievalTokens[a.ID], nil
}

func (s *Store) DeleteArtifact(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok {
		return os.ErrNotExist
	}
	delete(s.state.Artifacts, a.ID)
	token := s.state.RetrievalTokens[a.ID]
	delete(s.state.RetrievalTokens, a.ID)
	secret := s.state.EnrollmentSecrets[a.ID]
	delete(s.state.EnrollmentSecrets, a.ID)
	if err := s.save(); err != nil {
		s.state.Artifacts[a.ID] = a
		if token != "" {
			s.state.RetrievalTokens[a.ID] = token
		}
		if secret != "" {
			s.state.EnrollmentSecrets[a.ID] = secret
		}
		return err
	}
	return os.Remove(filepath.Join(s.root, "artifacts", a.Filename))
}

// OpenHosted checks the allowlist before opening an artifact. The returned file is
// a fixed artifact path, never a path supplied by an HTTP caller.
func (s *Store) OpenHosted(token string) (Artifact, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a Artifact
	found := false
	for id, candidate := range s.state.RetrievalTokens {
		if candidate == token {
			a = s.state.Artifacts[id]
			found = true
			break
		}
	}
	if !found || !a.Hosted || len(token) != 48 {
		return Artifact{}, nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.root, "artifacts", a.Filename))
	if err != nil {
		return Artifact{}, nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return Artifact{}, nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		f.Close()
		return Artifact{}, nil, errors.New("artifact SHA-256 mismatch")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return Artifact{}, nil, err
	}
	return a, f, nil
}

func VerifyFile(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("artifact SHA-256 mismatch")
	}
	return nil
}
