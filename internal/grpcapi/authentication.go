// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/token"
)

// authorizationHeader is the metadata a client sends its token in, as
// "Bearer TOKEN".
const authorizationHeader = "authorization"

// lastUseResolution is how precisely a token's last use is recorded, so
// that a busy token is written to disk once a minute, not on every call.
const lastUseResolution = time.Minute

// Authentication lets through only calls made with a token the daemon
// knows, as its TCP listener requires. It is safe for concurrent use.
type Authentication struct {
	tokenManager *token.Manager
	logger       *slog.Logger
}

// NewAuthentication returns an Authentication that checks tokens against
// those tokenManager knows, and logs the calls it refuses to logger.
func NewAuthentication(tokenManager *token.Manager, logger *slog.Logger) *Authentication {
	return &Authentication{tokenManager: tokenManager, logger: logger.With("component", "authentication")}
}

// UnaryInterceptor authenticates unary calls.
func (a *Authentication) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := a.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor authenticates streaming calls, before their first
// message.
func (a *Authentication) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := a.authenticate(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &authenticatedStream{ServerStream: ss, ctx: ctx})
	}
}

// authenticatedStream is a stream whose context holds the token its call
// was made with. It holds the context because a stream is how gRPC hands a
// handler its context.
type authenticatedStream struct {
	grpc.ServerStream

	ctx context.Context
}

// Context returns the stream's context, with its token.
func (s *authenticatedStream) Context() context.Context {
	return s.ctx
}

// authenticate returns ctx with the token the call was made with, or an
// Unauthenticated status if it has none the daemon knows. The
// caller is told nothing more: why its token was refused is for the
// daemon's log alone.
func (a *Authentication) authenticate(ctx context.Context, fullMethod string) (context.Context, error) {
	t, err := a.presentedToken(ctx)
	if err != nil {
		attrs := []any{"method", path.Base(fullMethod), "error", err}
		if p, ok := peer.FromContext(ctx); ok {
			attrs = append(attrs, "address", p.Addr.String())
		}
		a.logger.WarnContext(ctx, "call refused", attrs...)

		if errors.Is(err, errdefs.ErrUnauthenticated) {
			err = errdefs.Unauthenticated("unauthenticated")
		}
		return nil, toStatus(err)
	}

	a.recordUse(ctx, t)
	return context.WithValue(ctx, tokenKey{}, t), nil
}

// presentedToken returns the token a call was made with. It returns an
// ErrUnauthenticated error saying why if the call has none the daemon
// knows.
func (a *Authentication) presentedToken(ctx context.Context) (token.Token, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(authorizationHeader)
	if len(values) == 0 {
		return token.Token{}, errdefs.Unauthenticated("no authorization header")
	}

	value, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok {
		return token.Token{}, errdefs.Unauthenticated("authorization header is not Bearer")
	}
	secret, _, err := token.Parse(value)
	if err != nil {
		return token.Token{}, errdefs.Unauthenticated("%v", err)
	}

	t, err := a.tokenManager.TokenBySecret(secret)
	if errors.Is(err, errdefs.ErrNotFound) {
		return token.Token{}, errdefs.Unauthenticated("unknown token")
	}
	return t, err
}

// recordUse records that a token made a call, unless it was recorded less
// than lastUseResolution ago. A failure is logged, not returned: it is no
// reason to refuse the call.
func (a *Authentication) recordUse(ctx context.Context, t token.Token) {
	now := time.Now()
	if now.Sub(t.LastUsedAt) < lastUseResolution {
		return
	}

	err := a.tokenManager.RecordUse(t.ID, now.Truncate(lastUseResolution))
	if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
		a.logger.WarnContext(ctx, "cannot record a token's use", "token", t.Name, "error", err)
	}
}

// tokenKey is the context key of the token a call was made with.
type tokenKey struct{}

// tokenFrom returns the token a call was made with, and whether it was made
// with one: a call over the socket needs none.
func tokenFrom(ctx context.Context) (token.Token, bool) {
	t, ok := ctx.Value(tokenKey{}).(token.Token)
	return t, ok
}
