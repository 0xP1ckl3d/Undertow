package agentprofile

import (
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
}

type persisted struct {
	Profiles  map[string]Profile  `json:"profiles"`
	Artifacts map[string]Artifact `json:"artifacts"`
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
	s := &Store{root: root, templates: templates, version: version, state: persisted{Profiles: map[string]Profile{}, Artifacts: map[string]Artifact{}}}
	b, err := os.ReadFile(filepath.Join(root, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
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
	for id, a := range s.state.Artifacts {
		if id != a.ID || !safeArtifactFilename(a.Filename) {
			return nil, errors.New("invalid agent artifact record")
		}
	}
	return s, nil
}

func safeArtifactFilename(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\:")
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

func (s *Store) Build(name, platform, arch string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[name]
	if !ok {
		return Artifact{}, os.ErrNotExist
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
	filename := fmt.Sprintf("%s-%s-%s-%s", p.Name, platform, arch, id[:8])
	if platform == "windows" {
		filename += ".exe"
	}
	a := Artifact{ID: id, ProfileID: p.ID, Profile: p.Name, Server: p.Config.Server, Platform: platform, Architecture: arch, Filename: filename, Created: time.Now().UTC(), UndertowVersion: s.version, ProfileFormatVersion: p.Config.Version}
	e := Embedded{Profile: p, ArtifactID: id, Platform: platform, Architecture: arch, Created: a.Created, UndertowVersion: s.version}
	a.SHA256, a.Size, err = Stamp(path, filepath.Join(s.root, "artifacts", filename), e)
	if err != nil {
		return Artifact{}, err
	}
	s.state.Artifacts[id] = a
	if err := s.save(); err != nil {
		delete(s.state.Artifacts, id)
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
	a.Hosted = hosted
	s.state.Artifacts[a.ID] = a
	if err := s.save(); err != nil {
		s.state.Artifacts[a.ID] = old
		return Artifact{}, err
	}
	return a, nil
}
func (s *Store) Host(id string) (Artifact, error)   { return s.setHosted(id, true) }
func (s *Store) Unhost(id string) (Artifact, error) { return s.setHosted(id, false) }
func (s *Store) DeleteArtifact(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if !ok {
		return os.ErrNotExist
	}
	delete(s.state.Artifacts, a.ID)
	if err := s.save(); err != nil {
		s.state.Artifacts[a.ID] = a
		return err
	}
	return os.Remove(filepath.Join(s.root, "artifacts", a.Filename))
}

// OpenHosted checks the allowlist before opening an artifact. The returned file is
// a fixed artifact path, never a path supplied by an HTTP caller.
func (s *Store) OpenHosted(id, filename string) (Artifact, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.state.Artifacts[id]
	if !ok || !a.Hosted || a.Filename != filename {
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
