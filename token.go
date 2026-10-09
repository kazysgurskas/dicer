// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Tokens are the calls about the tokens a daemon's TCP listener accepts,
// reached as Client.Tokens.
type Tokens struct {
	api dicerdv1.DaemonServiceClient
}

// Token is a credential a client presents to a daemon's TCP listener, as
// WithToken takes it. Its value is returned only when the token is created
// or rotated: the daemon keeps only the SHA-256 of its secret.
type Token struct {
	// ID is the token's ID.
	ID string `json:"id,omitzero"`

	TokenSpec

	// CreateTime is when the token was made.
	CreateTime time.Time `json:"create_time,omitzero"`

	// UpdateTime is when the token was last rotated, or made if it has not
	// been.
	UpdateTime time.Time `json:"update_time,omitzero"`

	// LastUseTime is when the token last made a call, to the minute. It is
	// zero if the token never has.
	LastUseTime time.Time `json:"last_use_time,omitzero"`
}

// TokenSpec is what a token is.
type TokenSpec struct {
	// Name is the token's name.
	Name string `json:"name,omitzero"`

	// Scopes are what the token allows. Empty is ScopeAll. A call the
	// token's scopes do not allow fails with ErrPermissionDenied.
	Scopes []Scope `json:"scopes,omitzero"`

	// Secret is the token's secret: at least 32 letters and digits. Empty has
	// the daemon make one, which is what you want unless a tool must know the
	// secret before the token exists. It is never returned.
	Secret string `json:"-"`
}

// Scope is what a token allows: ScopeAll, or an action on a kind of
// resource, written RESOURCE:ACTION. A read scope allows the calls that only
// look at its resource. A write scope also allows those that change it. A
// token may make, rotate and delete only tokens whose scopes its own allow.
type Scope string

// ScopeAll allows everything, now and as the API grows.
const ScopeAll Scope = "*"

// The scopes of each kind of resource. Events, the log of what happens to
// every other resource, can only be read.
const (
	ScopeInstancesRead  Scope = "instances:read"
	ScopeInstancesWrite Scope = "instances:write"
	ScopeSnapshotsRead  Scope = "snapshots:read"
	ScopeSnapshotsWrite Scope = "snapshots:write"
	ScopeNetworksRead   Scope = "networks:read"
	ScopeNetworksWrite  Scope = "networks:write"
	ScopeVolumesRead    Scope = "volumes:read"
	ScopeVolumesWrite   Scope = "volumes:write"
	ScopeImagesRead     Scope = "images:read"
	ScopeImagesWrite    Scope = "images:write"
	ScopeKernelsRead    Scope = "kernels:read"
	ScopeKernelsWrite   Scope = "kernels:write"
	ScopeTokensRead     Scope = "tokens:read"
	ScopeTokensWrite    Scope = "tokens:write"
	ScopeEventsRead     Scope = "events:read"
)

// IssuedToken is a token with its value, as it is created or rotated.
type IssuedToken struct {
	Token

	// Value is what a client connects with, as WithToken takes it.
	Value string `json:"value"`
}

// Create makes a token, and returns it with its value, which is never
// returned again. It fails with ErrFailedPrecondition if the daemon is not
// served over TCP.
func (s *Tokens) Create(ctx context.Context, spec TokenSpec) (IssuedToken, error) {
	resp, err := s.api.CreateToken(ctx, &dicerdv1.CreateTokenRequest{
		Name:   spec.Name,
		Scopes: convertAll(spec.Scopes, func(s Scope) string { return string(s) }),
		Secret: spec.Secret,
	})
	if err != nil {
		return IssuedToken{}, fromStatus(err)
	}
	return issuedTokenFromProto(resp), nil
}

// List returns every token, without their values.
func (s *Tokens) List(ctx context.Context) ([]Token, error) {
	resp, err := s.api.ListTokens(ctx, &dicerdv1.ListTokensRequest{})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetTokens(), tokenFromProto), nil
}

// Get returns one token, without its value, or ErrNotFound.
func (s *Tokens) Get(ctx context.Context, name string) (Token, error) {
	resp, err := s.api.GetToken(ctx, &dicerdv1.GetTokenRequest{Name: name})
	if err != nil {
		return Token{}, fromStatus(err)
	}
	return tokenFromProto(resp), nil
}

// Rotate gives a token a new secret, which the daemon makes if secret is
// empty, and returns its new value. The old value stops working at once.
func (s *Tokens) Rotate(ctx context.Context, name, secret string) (IssuedToken, error) {
	resp, err := s.api.RotateToken(ctx, &dicerdv1.RotateTokenRequest{Name: name, Secret: secret})
	if err != nil {
		return IssuedToken{}, fromStatus(err)
	}
	return issuedTokenFromProto(resp), nil
}

// Delete removes a token. A client using it is refused from its next call.
func (s *Tokens) Delete(ctx context.Context, name string) error {
	_, err := s.api.DeleteToken(ctx, &dicerdv1.DeleteTokenRequest{Name: name})
	return fromStatus(err)
}

// tokenFromProto returns the token p describes.
func tokenFromProto(p *dicerdv1.Token) Token {
	return Token{
		ID: p.GetId(),
		TokenSpec: TokenSpec{
			Name:   p.GetName(),
			Scopes: convertAll(p.GetScopes(), func(s string) Scope { return Scope(s) }),
		},
		CreateTime:  timeFromProto(p.GetCreateTime()),
		UpdateTime:  timeFromProto(p.GetUpdateTime()),
		LastUseTime: timeFromProto(p.GetLastUseTime()),
	}
}

// issuedTokenFromProto returns the token and value p holds.
func issuedTokenFromProto(p *dicerdv1.IssuedToken) IssuedToken {
	return IssuedToken{Token: tokenFromProto(p.GetToken()), Value: p.GetValue()}
}
