package pivot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"undertow/internal/authcontext"
	"undertow/internal/mux"
)

const TokenDestination = "tokens.undertow.invalid:0"

// This store belongs to the agent process, across transport reconnects. It is
// never installed in the server Manager or serialized.
var agentTokens = authcontext.New(authcontext.ResolvingBackend{Backend: authcontext.WindowsBackend{}})
var agentCreationKeys authcontext.CreationKeys

type tokenContextKey struct{}

func WithTokenContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tokenContextKey{}, id)
}
func TokenContextID(ctx context.Context) string {
	id, _ := ctx.Value(tokenContextKey{}).(string)
	return id
}
func ValidateTokenContextID(id string) error {
	if id != "" && id != "process" && !authcontext.ValidID(id) {
		return errors.New("invalid authentication context ID")
	}
	return nil
}

type tokenLeaseKey struct{}
type tokenCapabilityKey struct{}

func operationTokenMetadata(ctx context.Context) (authcontext.Metadata, bool, error) {
	token, ok := ctx.Value(tokenLeaseKey{}).(authcontext.Token)
	if !ok {
		return authcontext.Metadata{}, false, nil
	}
	metadata, err := token.Metadata()
	return metadata, true, err
}

func acquireTokenContext(ctx context.Context, id string) (context.Context, func(), error) {
	if id == "" || id == "process" {
		return ctx, func() {}, nil
	}
	if !authcontext.ValidID(id) {
		return ctx, nil, errors.New("invalid authentication context ID")
	}
	allowed, _ := ctx.Value(tokenCapabilityKey{}).(bool)
	if !allowed {
		return ctx, nil, errors.New("agent authentication contexts are disabled")
	}
	t, err := agentTokens.Acquire(id)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, tokenLeaseKey{}, t), func() { _ = t.Close() }, nil
}

type TokenRequest struct {
	Action      string                   `json:"action"`
	ID          string                   `json:"id,omitempty"`
	SealedLogon *authcontext.SealedLogon `json:"sealed_logon,omitempty"`
}
type TokenResponse struct {
	StoreInstanceID  string                   `json:"store_instance_id,omitempty"`
	CreationKey      *authcontext.CreationKey `json:"creation_key,omitempty"`
	Contexts         []authcontext.Metadata   `json:"contexts"`
	Candidates       []authcontext.Metadata   `json:"candidates,omitempty"`
	Created          *authcontext.Metadata    `json:"created,omitempty"`
	DefaultContextID string                   `json:"default_context_id,omitempty"`
	Error            string                   `json:"error,omitempty"`
}

func ManageTokens(ctx context.Context, agent *mux.Mux, r TokenRequest) (TokenResponse, error) {
	if agent == nil {
		return TokenResponse{}, errors.New("agent is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := agent.Open(ctx, TokenDestination)
	if err != nil {
		return TokenResponse{}, err
	}
	return ManageTokensOnStream(ctx, s, r)
}

// ManageTokensOnStream runs a token request over a stream opened after the
// control plane has waited for a sleeping agent's authenticated check-in.
func ManageTokensOnStream(ctx context.Context, s *mux.Stream, r TokenRequest) (TokenResponse, error) {
	defer s.Close()
	var err error
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-done:
		}
	}()
	if err = json.NewEncoder(s).Encode(r); err != nil {
		return TokenResponse{}, err
	}
	if err = s.CloseWrite(); err != nil {
		return TokenResponse{}, err
	}
	var result TokenResponse
	err = json.NewDecoder(io.LimitReader(s, 512<<10)).Decode(&result)
	if err == nil {
		_, err = io.Copy(io.Discard, s)
	}
	if err == nil && result.Error != "" {
		err = errors.New(result.Error)
	}
	return result, err
}
func serveTokens(ctx context.Context, s *mux.Stream) {
	defer s.Close()
	if s.AcceptOpen(ctx) != nil {
		return
	}
	timer := time.AfterFunc(30*time.Second, func() { _ = s.Close() })
	defer timer.Stop()
	var r TokenRequest
	d := json.NewDecoder(io.LimitReader(s, 8192))
	d.DisallowUnknownFields()
	response := TokenResponse{}
	var err error
	if d.Decode(&r) != nil {
		err = errors.New("invalid authentication context request")
	} else {
		switch r.Action {
		case "list":
		case "discover":
			response.Candidates, err = agentTokens.Discover(ctx)
		case "import":
			var m authcontext.Metadata
			m, err = agentTokens.Import(r.ID)
			if err == nil {
				response.Created = &m
			}
		case "creation-key":
			var key authcontext.CreationKey
			key, err = agentCreationKeys.Issue()
			if err == nil {
				response.CreationKey = &key
			}
		case "create":
			if r.SealedLogon == nil {
				err = errors.New("sealed agent logon required")
			} else {
				var request authcontext.LogonRequest
				request, err = agentCreationKeys.Open(*r.SealedLogon)
				if err == nil {
					var m authcontext.Metadata
					m, err = agentTokens.Create(ctx, request)
					request.Password = ""
					if err == nil {
						response.Created = &m
					}
				}
			}
		case "remove":
			err = agentTokens.Remove(r.ID)
		case "clear":
			agentTokens.Clear()
			agentCreationKeys.Clear()
		default:
			err = errors.New("unknown authentication context action")
		}
	}
	if err != nil {
		response.Error = err.Error()
	}
	response.Contexts = agentTokens.List()
	response.Candidates = agentTokens.Candidates()
	response.StoreInstanceID = agentTokens.InstanceID()
	_ = json.NewEncoder(s).Encode(response)
	_ = s.CloseWrite()
}
