package control

import "context"

// ActionClaims carry an action ID and UI source. Operator identity supplied by
// a client is ignored; the server obtains it from the authenticated session.
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
