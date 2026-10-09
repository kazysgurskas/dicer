// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/token"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// withAuthorization returns a context carrying an authorization header, as a
// call arrives with it.
func withAuthorization(ctx context.Context, header string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(authorizationHeader, header))
}

func TestAuthenticationLetsInOnlyAKnownToken(t *testing.T) {
	h, store := newTokenHandler(t)
	issued, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	value := issued.GetValue()
	stranger := token.Format(token.NewSecret(), testFingerprint)

	var log bytes.Buffer
	a := NewAuthentication(h.tokenManager, slog.New(slog.NewTextHandler(&log, nil)))
	info := &grpc.UnaryServerInfo{FullMethod: "/dicerd.v1.DaemonService/GetHostInfo"}

	// A refused caller is told no more than that; the log says why.
	tests := []struct {
		name string
		ctx  context.Context
		want codes.Code
		logs string
	}{
		{"no header", t.Context(), codes.Unauthenticated, "no authorization header"},
		{"another scheme", withAuthorization(t.Context(), "Basic "+value), codes.Unauthenticated, "not Bearer"},
		{"not a token", withAuthorization(t.Context(), "Bearer hunter2"), codes.Unauthenticated, "invalid token"},
		{"a token it does not know", withAuthorization(t.Context(), "Bearer "+stranger), codes.Unauthenticated, "unknown token"},
		{"its token", withAuthorization(t.Context(), "Bearer "+value), codes.OK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log.Reset()
			var name string
			handler := func(ctx context.Context, _ any) (any, error) {
				caller, _ := tokenFrom(ctx)
				name = caller.Name
				return &emptypb.Empty{}, nil
			}

			_, err := a.UnaryInterceptor()(tt.ctx, &dicerdv1.GetHostInfoRequest{}, info, handler)
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v, want %v (%v)", got, tt.want, err)
			}
			if tt.want != codes.OK && status.Convert(err).Message() != "unauthenticated" {
				t.Errorf("message = %q, want unauthenticated alone", status.Convert(err).Message())
			}
			if !strings.Contains(log.String(), tt.logs) {
				t.Errorf("log = %q, want it to say %q", log.String(), tt.logs)
			}
			if tt.want == codes.OK && name != "ci" {
				t.Errorf("the handler saw the token %q, want ci", name)
			}
			if tt.want != codes.OK && name != "" {
				t.Error("the handler ran for a refused call")
			}
		})
	}

	used, err := store.Token("ci")
	if err != nil {
		t.Fatal(err)
	}
	if used.LastUsedAt.IsZero() {
		t.Error("the token's use was not recorded")
	}
}

func TestAuthenticationLetsInOnlyAStreamWithAKnownToken(t *testing.T) {
	h, _ := newTokenHandler(t)
	issued, err := h.CreateToken(t.Context(), &dicerdv1.CreateTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}

	a := NewAuthentication(h.tokenManager, slog.New(slog.DiscardHandler))
	info := &grpc.StreamServerInfo{FullMethod: "/dicerd.v1.DaemonService/GetEvents"}

	var name string
	handler := func(_ any, stream grpc.ServerStream) error {
		caller, _ := tokenFrom(stream.Context())
		name = caller.Name
		return nil
	}

	ss := &fakeServerStream{ctx: withAuthorization(t.Context(), "Bearer "+issued.GetValue())}
	if err := a.StreamInterceptor()(nil, ss, info, handler); err != nil {
		t.Fatalf("a stream with its token: %v", err)
	}
	if name != "ci" {
		t.Errorf("the handler saw the token %q, want ci", name)
	}

	ss = &fakeServerStream{ctx: t.Context()}
	if err := a.StreamInterceptor()(nil, ss, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Errorf("a stream without a token = %v, want Unauthenticated", err)
	}
}
