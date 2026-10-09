package authcontext

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeToken struct {
	closed atomic.Bool
	closes *atomic.Int32
	fail   bool
}

func (t *fakeToken) Metadata() (Metadata, error) {
	if t.fail {
		return Metadata{}, errors.New("metadata unavailable")
	}
	return Metadata{Identity: "test\\operator", Source: "test fixture", TokenType: "primary", ImpersonationLevel: "not-applicable", IntegrityLevel: "medium"}, nil
}
func (t *fakeToken) Duplicate() (Token, error) {
	if t.closed.Load() {
		return nil, errors.New("closed")
	}
	return &fakeToken{closes: t.closes, fail: t.fail}, nil
}
func (t *fakeToken) Close() error {
	if !t.closed.Swap(true) {
		t.closes.Add(1)
	}
	return nil
}

type fakeBackend struct {
	closes atomic.Int32
	fail   bool
}

func (b *fakeBackend) Discover(context.Context) ([]Token, error) {
	return []Token{&fakeToken{closes: &b.closes}}, nil
}
func (b *fakeBackend) Logon(context.Context, LogonRequest) (Token, error) {
	return &fakeToken{closes: &b.closes, fail: b.fail}, nil
}

func TestStoreLifecycleAndIndependentLeases(t *testing.T) {
	b := &fakeBackend{}
	s := New(b)
	defer s.Clear()
	candidates, err := s.Discover(context.Background())
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	m, err := s.Import(candidates[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(m.ID) || m.ID == candidates[0].ID || m.CreatedAt.IsZero() {
		t.Fatal(m)
	}
	lease, err := s.Acquire(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Remove(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Acquire(m.ID); err == nil {
		t.Fatal("removed context accepted")
	}
	duplicate, err := lease.Duplicate()
	if err != nil {
		t.Fatal("removal closed active operation", err)
	}
	_ = duplicate.Close()
	_ = lease.Close()
	s.Clear()
	if len(s.List()) != 0 {
		t.Fatal("store not cleared")
	}
	if _, err = s.Import(candidates[0].ID); err == nil {
		t.Fatal("candidate survived clear")
	}
	if b.closes.Load() != 4 {
		t.Fatal("lifecycle leaked token resources", b.closes.Load())
	}
}
func TestDiscoveryReplacementExpiryAndFailureCleanup(t *testing.T) {
	b := &fakeBackend{}
	s := New(b)
	defer s.Clear()
	first, _ := s.Discover(context.Background())
	second, _ := s.Discover(context.Background())
	if _, err := s.Import(first[0].ID); err == nil {
		t.Fatal("rediscovery retained stale candidate")
	}
	s.mu.Lock()
	e := s.candidates[second[0].ID]
	e.expires = time.Now().Add(-time.Second)
	s.candidates[second[0].ID] = e
	s.mu.Unlock()
	if _, err := s.Import(second[0].ID); err == nil {
		t.Fatal("expired candidate accepted")
	}
	expired := s.Candidates()
	if len(expired) != 1 || expired[0].State != "expired" || expired[0].ExpiresAt.IsZero() {
		t.Fatal("expired candidate metadata was not retained", expired)
	}
	if b.closes.Load() != 2 {
		t.Fatal("candidate handles not closed", b.closes.Load())
	}
	b.fail = true
	if _, err := s.Create(context.Background(), LogonRequest{}); err == nil {
		t.Fatal("metadata failure accepted")
	}
	if b.closes.Load() != 3 {
		t.Fatal("failed import leaked token")
	}
}
func TestStoreConcurrentRemovalAndMetadataBoundary(t *testing.T) {
	b := &fakeBackend{}
	s := New(b)
	defer s.Clear()
	m, err := s.Create(context.Background(), LogonRequest{Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lease, err := s.Acquire(m.ID); err == nil {
				defer lease.Close()
				if _, err := lease.Metadata(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	_ = s.Remove(m.ID)
	wg.Wait()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-secret", "handle", "password", "credential"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("sensitive metadata", string(data))
		}
	}
	if data, _ := json.Marshal(s); string(data) != "{}" {
		t.Fatal("store serialized private state", string(data))
	}
}
func TestStoreLimitAndOpaqueIDValidation(t *testing.T) {
	b := &fakeBackend{}
	s := New(b)
	defer s.Clear()
	for i := 0; i < Limit; i++ {
		if _, err := s.Create(context.Background(), LogonRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(context.Background(), LogonRequest{}); err == nil {
		t.Fatal("limit bypassed")
	}
	for _, id := range []string{"", "42", "process", strings.Repeat("G", 32), strings.Repeat("a", 31)} {
		if ValidID(id) {
			t.Fatal("invalid ID", id)
		}
	}
}
