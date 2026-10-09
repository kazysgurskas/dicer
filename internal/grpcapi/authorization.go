// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"path"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/types"
)

// methodScopes are the scope each call needs, by method. Every method of the
// API is here: one missing is refused to every token, so that a call added
// to the API is not open by mistake. The zero Scope lets any token make the
// call.
//
// A call needs the scope of the resource it is about. Exec and copying go
// into the guest, so they need instances:write either way, and a call that
// makes or changes an instance from a snapshot needs instances:write too.
var methodScopes = map[string]types.Scope{
	"CreateInstance":         types.ScopeInstancesWrite,
	"UpdateInstance":         types.ScopeInstancesWrite,
	"RenameInstance":         types.ScopeInstancesWrite,
	"StartInstance":          types.ScopeInstancesWrite,
	"StopInstance":           types.ScopeInstancesWrite,
	"PauseInstance":          types.ScopeInstancesWrite,
	"ResumeInstance":         types.ScopeInstancesWrite,
	"StandbyInstance":        types.ScopeInstancesWrite,
	"ForkInstance":           types.ScopeInstancesWrite,
	"ResizeInstance":         types.ScopeInstancesWrite,
	"DeleteInstance":         types.ScopeInstancesWrite,
	"ListInstances":          types.ScopeInstancesRead,
	"GetInstance":            types.ScopeInstancesRead,
	"GetInstanceLogs":        types.ScopeInstancesRead,
	"GetInstanceStats":       types.ScopeInstancesRead,
	"ListInstanceProcesses":  types.ScopeInstancesRead,
	"ExecInstance":           types.ScopeInstancesWrite,
	"CopyToInstance":         types.ScopeInstancesWrite,
	"CopyFromInstance":       types.ScopeInstancesWrite,
	"CreateSnapshot":         types.ScopeSnapshotsWrite,
	"ListSnapshots":          types.ScopeSnapshotsRead,
	"GetSnapshot":            types.ScopeSnapshotsRead,
	"DeleteSnapshot":         types.ScopeSnapshotsWrite,
	"RestoreSnapshot":        types.ScopeInstancesWrite,
	"ForkSnapshot":           types.ScopeInstancesWrite,
	"CreateNetwork":          types.ScopeNetworksWrite,
	"ListNetworks":           types.ScopeNetworksRead,
	"GetNetwork":             types.ScopeNetworksRead,
	"DeleteNetwork":          types.ScopeNetworksWrite,
	"ListNetworkAllocations": types.ScopeNetworksRead,
	"CreateVolume":           types.ScopeVolumesWrite,
	"ListVolumes":            types.ScopeVolumesRead,
	"GetVolume":              types.ScopeVolumesRead,
	"DeleteVolume":           types.ScopeVolumesWrite,
	"PullImage":              types.ScopeImagesWrite,
	"ListImages":             types.ScopeImagesRead,
	"GetImage":               types.ScopeImagesRead,
	"DeleteImage":            types.ScopeImagesWrite,
	"PruneImages":            types.ScopeImagesWrite,
	"ImportKernel":           types.ScopeKernelsWrite,
	"ListKernels":            types.ScopeKernelsRead,
	"GetKernel":              types.ScopeKernelsRead,
	"DeleteKernel":           types.ScopeKernelsWrite,
	"CreateToken":            types.ScopeTokensWrite,
	"ListTokens":             types.ScopeTokensRead,
	"GetToken":               types.ScopeTokensRead,
	"RotateToken":            types.ScopeTokensWrite,
	"DeleteToken":            types.ScopeTokensWrite,
	"GetHostInfo":            "",
	"GetResources":           types.ScopeInstancesRead,
	"GetEvents":              types.ScopeEventsRead,
}

// UnaryAuthorizationInterceptor refuses a unary call its token's scopes do
// not allow. It belongs after authentication, which puts the token in the
// call's context: a call without one is refused.
func UnaryAuthorizationInterceptor(
	ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
	if err := authorize(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// StreamAuthorizationInterceptor refuses a streaming call its token's scopes
// do not allow, as UnaryAuthorizationInterceptor does.
func StreamAuthorizationInterceptor(
	srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
) error {
	if err := authorize(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}

// authorize returns an ErrPermissionDenied error unless the call's token
// has the scope its method needs.
func authorize(ctx context.Context, fullMethod string) error {
	method := path.Base(fullMethod)
	scope, known := methodScopes[method]
	t, ok := tokenFrom(ctx)

	switch {
	case !ok:
		return errdefs.PermissionDenied("%s needs a token", method)
	case !known:
		return errdefs.PermissionDenied("%s is not open to tokens", method)
	case scope != "" && !t.Allows(scope):
		return errdefs.PermissionDenied("token %q lacks scope %s", t.Name, scope)
	}
	return nil
}
