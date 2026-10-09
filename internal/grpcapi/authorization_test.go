// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/types"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// TestEveryMethodNeedsAScope keeps methodScopes in step with the API: a
// method added without a scope would be refused to every token, and one
// left behind after a method is removed would mean nothing.
func TestEveryMethodNeedsAScope(t *testing.T) {
	service := dicerdv1.File_dicerd_v1_dicerd_proto.Services().ByName("DaemonService")

	methods := make(map[string]bool)
	for i := range service.Methods().Len() {
		method := string(service.Methods().Get(i).Name())
		methods[method] = true

		scope, ok := methodScopes[method]
		switch {
		case !ok:
			t.Errorf("%s has no entry in methodScopes", method)
		case scope != "":
			if err := scope.Validate(); err != nil {
				t.Errorf("%s: %v", method, err)
			}
		}
	}

	for method := range methodScopes {
		if !methods[method] {
			t.Errorf("methodScopes names %s, which the API does not have", method)
		}
	}
}

func TestAuthorizationAllowsOnlyWhatTheTokensScopesDo(t *testing.T) {
	tests := []struct {
		name   string
		scopes []types.Scope
		method string
		want   error
	}{
		{"everything", []types.Scope{types.ScopeAll}, "DeleteInstance", nil},
		{"its scope", []types.Scope{"instances:write"}, "DeleteInstance", nil},
		{"reading what it may write", []types.Scope{"instances:write"}, "ListInstances", nil},
		{"writing what it may read", []types.Scope{"instances:read"}, "DeleteInstance", errdefs.ErrPermissionDenied},
		{"another resource", []types.Scope{"volumes:write"}, "ListInstances", errdefs.ErrPermissionDenied},
		{"exec, which reaches into the guest", []types.Scope{"instances:read"}, "ExecInstance", errdefs.ErrPermissionDenied},
		{"host info, which any token may ask", []types.Scope{"kernels:read"}, "GetHostInfo", nil},
		{"events, which have a scope of their own", []types.Scope{"instances:write"}, "GetEvents", errdefs.ErrPermissionDenied},
		{"events, with it", []types.Scope{"events:read"}, "GetEvents", nil},
		{"the host's capacity, which shows instances", []types.Scope{"instances:read"}, "GetResources", nil},
		{"a method it does not know", []types.Scope{types.ScopeAll}, "LaunchMissiles", errdefs.ErrPermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), tokenKey{}, types.Token{Name: "ci", Scopes: tt.scopes})
			info := &grpc.UnaryServerInfo{FullMethod: "/dicerd.v1.DaemonService/" + tt.method}

			called := false
			handler := func(context.Context, any) (any, error) {
				called = true
				return &emptypb.Empty{}, nil
			}

			_, err := UnaryAuthorizationInterceptor(ctx, nil, info, handler)
			switch {
			case tt.want == nil && err != nil:
				t.Errorf("%s with %v = %v, want it allowed", tt.method, tt.scopes, err)
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Errorf("%s with %v = %v, want %v", tt.method, tt.scopes, err, tt.want)
			case tt.want != nil && called:
				t.Error("the handler ran for a refused call")
			}
		})
	}
}

// TestAuthorizationRefusesACallWithoutAToken checks that a call reaching
// authorization without authentication having run is refused, not let
// through.
func TestAuthorizationRefusesACallWithoutAToken(t *testing.T) {
	info := &grpc.StreamServerInfo{FullMethod: "/dicerd.v1.DaemonService/GetHostInfo"}
	handler := func(any, grpc.ServerStream) error { return nil }

	err := StreamAuthorizationInterceptor(nil, &fakeServerStream{ctx: t.Context()}, info, handler)
	wantClass(t, err, errdefs.ErrPermissionDenied)
}
