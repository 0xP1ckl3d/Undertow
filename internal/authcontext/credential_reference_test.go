package authcontext

import (
	"context"
	"errors"
	"testing"
)

type fixtureResolver struct {
	called bool
	fail   bool
}

func (r *fixtureResolver) ResolveLogon(_ context.Context, id string) (LogonRequest, error) {
	r.called = true
	if r.fail || id != "fixture-reference" {
		return LogonRequest{}, errors.New("fixture provider error containing sensitive details")
	}
	return LogonRequest{User: "fixture-user", Password: "fixture-secret", LogonType: "interactive"}, nil
}

func TestCredentialReferenceExtensionRejectsUnavailableAndMixedInput(t *testing.T) {
	backend := &fakeBackend{}
	r := LogonRequest{Reference: "fixture-reference", LogonType: "batch"}
	if _, err := (ResolvingBackend{Backend: backend}).Logon(context.Background(), r); err == nil {
		t.Fatal("unconfigured reference accepted")
	}
	resolver := &fixtureResolver{}
	wrapped := ResolvingBackend{Backend: backend, Credentials: resolver}
	r.Password = "fixture-secret"
	if _, err := wrapped.Logon(context.Background(), r); err == nil || resolver.called {
		t.Fatal("mixed supplied credentials and reference accepted")
	}
	r.Password = ""
	token, err := wrapped.Logon(context.Background(), r)
	if err != nil || !resolver.called {
		t.Fatal("reference could not create an ordinary token", err)
	}
	_ = token.Close()
	resolver.fail = true
	if _, err = wrapped.Logon(context.Background(), r); err == nil || err.Error() != "agent credential reference unavailable" {
		t.Fatal("provider details leaked into token error")
	}
}
