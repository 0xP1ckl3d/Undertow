package authcontext

import (
	"context"
	"errors"
)

// CredentialResolver is an agent-side extension point. Implementations resolve
// an opaque credential reference only in volatile agent memory. No Credential
// Store or persistence implementation is included here.
type CredentialResolver interface {
	ResolveLogon(context.Context, string) (LogonRequest, error)
}

// ResolvingBackend can wrap any token backend without changing Store, IDs,
// leases or metadata. Agents without a resolver reject credential references.
type ResolvingBackend struct {
	Backend
	Credentials CredentialResolver
}

func (b ResolvingBackend) Logon(ctx context.Context, r LogonRequest) (Token, error) {
	if r.Reference == "" {
		return b.Backend.Logon(ctx, r)
	}
	if b.Credentials == nil {
		return nil, errors.New("agent credential provider is not configured")
	}
	if r.User != "" || r.Domain != "" || r.Password != "" {
		return nil, errors.New("choose a credential reference or supplied logon material")
	}
	resolved, err := b.Credentials.ResolveLogon(ctx, r.Reference)
	if err != nil {
		return nil, errors.New("agent credential reference unavailable")
	}
	defer func() { resolved.Password = "" }()
	resolved.Reference = ""
	if r.LogonType != "" {
		resolved.LogonType = r.LogonType
	}
	return b.Backend.Logon(ctx, resolved)
}
