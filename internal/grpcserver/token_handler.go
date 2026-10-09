// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/token"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// tokenHandler handles token-related RPCs.
type tokenHandler struct {
	tokenManager *token.Manager
	// servesTCP reports whether the daemon is served over TCP, the only
	// place a token is any use.
	servesTCP bool
	// fingerprint is what every token carries for clients to check the
	// daemon by, or empty if they check its certificate for themselves.
	fingerprint string
}

// CreateToken records a new token and returns its value, the only time it
// is returned. A request that names no scopes gets every one.
func (h *tokenHandler) CreateToken(ctx context.Context, req *dicerdv1.CreateTokenRequest) (*dicerdv1.IssuedToken, error) {
	if err := h.checkServesTCP(); err != nil {
		return nil, err
	}

	scopes := make([]token.Scope, 0, len(req.GetScopes()))
	for _, scope := range req.GetScopes() {
		scopes = append(scopes, token.Scope(scope))
	}
	if len(scopes) == 0 {
		scopes = []token.Scope{token.ScopeAll}
	}
	if err := checkCallerAllows(ctx, token.Token{Name: req.GetName(), Scopes: scopes}); err != nil {
		return nil, err
	}

	t, secret, err := h.tokenManager.Create(req.GetName(), req.GetSecret(), scopes)
	if err != nil {
		return nil, err
	}
	return &dicerdv1.IssuedToken{Token: tokenToProto(t), Value: token.Format(secret, h.fingerprint)}, nil
}

// ListTokens lists the tokens, sorted by name, without their secrets.
func (h *tokenHandler) ListTokens(context.Context, *dicerdv1.ListTokensRequest) (*dicerdv1.ListTokensResponse, error) {
	tokens := h.tokenManager.Tokens()

	out := make([]*dicerdv1.Token, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, tokenToProto(t))
	}
	return &dicerdv1.ListTokensResponse{Tokens: out}, nil
}

// GetToken returns a token, without its secret.
func (h *tokenHandler) GetToken(_ context.Context, req *dicerdv1.GetTokenRequest) (*dicerdv1.Token, error) {
	t, err := h.tokenManager.Token(req.GetName())
	if err != nil {
		return nil, err
	}
	return tokenToProto(t), nil
}

// RotateToken gives a token a new secret and returns its new value. The old
// value no longer works.
func (h *tokenHandler) RotateToken(ctx context.Context, req *dicerdv1.RotateTokenRequest) (*dicerdv1.IssuedToken, error) {
	if err := h.checkServesTCP(); err != nil {
		return nil, err
	}
	t, err := h.tokenManager.Token(req.GetName())
	if err != nil {
		return nil, err
	}
	if err := checkCallerAllows(ctx, t); err != nil {
		return nil, err
	}

	t, secret, err := h.tokenManager.Rotate(t.ID, req.GetSecret())
	if err != nil {
		return nil, err
	}
	return &dicerdv1.IssuedToken{Token: tokenToProto(t), Value: token.Format(secret, h.fingerprint)}, nil
}

// DeleteToken removes a token, which no longer works.
func (h *tokenHandler) DeleteToken(ctx context.Context, req *dicerdv1.DeleteTokenRequest) (*emptypb.Empty, error) {
	t, err := h.tokenManager.Token(req.GetName())
	if err != nil {
		return nil, err
	}
	if err := checkCallerAllows(ctx, t); err != nil {
		return nil, err
	}
	if err := h.tokenManager.Delete(t.ID); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// checkCallerAllows returns an ErrPermissionDenied error unless the call's
// token allows every scope t has, so that no token makes, rotates or
// deletes one that can do more than it can. A call over the socket has no
// token, and may.
func checkCallerAllows(ctx context.Context, t token.Token) error {
	caller, ok := tokenFrom(ctx)
	if ok && !caller.AllowsAll(t.Scopes) {
		return errdefs.PermissionDenied("token %q lacks some of the scopes of token %q", caller.Name, t.Name)
	}
	return nil
}

// checkServesTCP returns an ErrInvalidState error if the daemon is not
// served over TCP, so that no token is made that nothing could use.
func (h *tokenHandler) checkServesTCP() error {
	if h.servesTCP {
		return nil
	}
	return errdefs.InvalidState("tokens are for the daemon's TCP listener, which is off: " +
		"set server.listen in the daemon's configuration and restart it")
}
