// Package authcontext owns live authentication objects in the agent process.
// Only Metadata and opaque IDs may cross the agent boundary.
package authcontext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

const Limit = 128
const CandidateTTL = 2 * time.Minute

type Metadata struct {
	ID                 string    `json:"id"`
	Identity           string    `json:"identity"`
	Domain             string    `json:"domain"`
	User               string    `json:"user"`
	TokenType          string    `json:"token_type"`
	ImpersonationLevel string    `json:"impersonation_level"`
	IntegrityLevel     string    `json:"integrity_level"`
	SessionID          uint32    `json:"session_id"`
	Elevated           bool      `json:"elevated"`
	ElevationType      string    `json:"elevation_type"`
	Source             string    `json:"source"`
	CreatedAt          time.Time `json:"created_at"`
}

// LogonRequest is transient transport input, never a queued job or history DTO.
// A future agent-side credential provider can resolve Reference and call Logon.
type LogonRequest struct {
	User      string `json:"user"`
	Domain    string `json:"domain,omitempty"`
	Password  string `json:"password,omitempty"`
	LogonType string `json:"logon_type"`
	Reference string `json:"credential_reference,omitempty"`
}

type Token interface {
	Metadata() (Metadata, error)
	Duplicate() (Token, error)
	Close() error
}
type Backend interface {
	Discover(context.Context) ([]Token, error)
	Logon(context.Context, LogonRequest) (Token, error)
}

type entry struct {
	token    Token
	metadata Metadata
	expires  time.Time
}
type Store struct {
	mu         sync.Mutex
	backend    Backend
	contexts   map[string]entry
	candidates map[string]entry
}

func New(backend Backend) *Store {
	return &Store{backend: backend, contexts: map[string]entry{}, candidates: map[string]entry{}}
}
func ValidID(id string) bool { return len(id) == 32 && lenTrimHex(id) == 0 }
func lenTrimHex(id string) int {
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return 1
		}
	}
	return 0
}
func opaqueID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (s *Store) expireLocked() {
	for id, e := range s.candidates {
		if !time.Now().Before(e.expires) {
			_ = e.token.Close()
			delete(s.candidates, id)
		}
	}
}
func (s *Store) List() []Metadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	return metadataList(s.contexts)
}
func metadataList(entries map[string]entry) []Metadata {
	result := make([]Metadata, 0, len(entries))
	for _, e := range entries {
		result = append(result, e.metadata)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *Store) Discover(ctx context.Context) ([]Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.candidates {
		_ = e.token.Close()
		delete(s.candidates, id)
	}
	tokens, err := s.backend.Discover(ctx)
	if err != nil {
		for _, t := range tokens {
			_ = t.Close()
		}
		return nil, err
	}
	for _, t := range tokens {
		if len(s.candidates) >= Limit {
			_ = t.Close()
			continue
		}
		m, err := t.Metadata()
		if err != nil {
			_ = t.Close()
			continue
		}
		id, err := opaqueID()
		if err != nil {
			_ = t.Close()
			continue
		}
		m.ID, m.CreatedAt = id, time.Now().UTC()
		s.candidates[id] = entry{token: t, metadata: m, expires: time.Now().Add(CandidateTTL)}
	}
	time.AfterFunc(CandidateTTL, func() { s.mu.Lock(); defer s.mu.Unlock(); s.expireLocked() })
	return metadataList(s.candidates), nil
}
func (s *Store) addLocked(t Token) (Metadata, error) {
	if len(s.contexts) >= Limit {
		_ = t.Close()
		return Metadata{}, errors.New("authentication context limit reached")
	}
	m, err := t.Metadata()
	if err != nil {
		_ = t.Close()
		return Metadata{}, err
	}
	id, err := opaqueID()
	if err != nil {
		_ = t.Close()
		return Metadata{}, err
	}
	m.ID, m.CreatedAt = id, time.Now().UTC()
	s.contexts[id] = entry{token: t, metadata: m}
	return m, nil
}
func (s *Store) Import(id string) (Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	e, ok := s.candidates[id]
	if !ok {
		return Metadata{}, errors.New("token candidate unavailable; discover again")
	}
	t, err := e.token.Duplicate()
	if err != nil {
		return Metadata{}, err
	}
	return s.addLocked(t)
}
func (s *Store) Create(ctx context.Context, request LogonRequest) (Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.contexts) >= Limit {
		return Metadata{}, errors.New("authentication context limit reached")
	}
	t, err := s.backend.Logon(ctx, request)
	if err != nil {
		return Metadata{}, err
	}
	return s.addLocked(t)
}

// Acquire duplicates under the lock. Removal prevents new operations; already
// accepted operations retain their independent lease until completion.
func (s *Store) Acquire(id string) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.contexts[id]
	if !ok {
		return nil, errors.New("authentication context unavailable; select or import a live context")
	}
	return e.token.Duplicate()
}
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.contexts[id]
	if !ok {
		return errors.New("authentication context unavailable")
	}
	delete(s.contexts, id)
	return e.token.Close()
}
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.contexts {
		_ = e.token.Close()
		delete(s.contexts, id)
	}
	for id, e := range s.candidates {
		_ = e.token.Close()
		delete(s.candidates, id)
	}
}
