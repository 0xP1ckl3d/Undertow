package control

import "context"

// ActionClaims are supplied by the operator client. The server binds the
// authenticated client ID and session separately; these operator fields remain
// unverified until server authentication is introduced.
type ActionClaims struct {
	ActionID    string `json:"action_id,omitempty"`
	OperatorID  string `json:"operator_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Source      string `json:"source,omitempty"`
}

type actionContextKey struct{}

type actionContext struct {
	ActionClaims
	ClientID        string
	ClientSessionID uint64
}

func WithActionClaims(ctx context.Context, claims ActionClaims) context.Context {
	return context.WithValue(ctx, actionContextKey{}, actionContext{ActionClaims: claims})
}

func claimsFromContext(ctx context.Context) ActionClaims {
	actor, _ := ctx.Value(actionContextKey{}).(actionContext)
	return actor.ActionClaims
}

func boundActionFromContext(ctx context.Context) actionContext {
	actor, _ := ctx.Value(actionContextKey{}).(actionContext)
	return actor
}
