// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Audit logs every call that can change something: who made it, the
// resource it named, and how it ended.
type Audit struct {
	logger *slog.Logger
}

// NewAudit returns an Audit that logs to logger.
func NewAudit(logger *slog.Logger) Audit {
	return Audit{logger: logger.With("component", "audit")}
}

// UnixPeer is the process at the other end of a Unix socket connection, as
// the kernel reports it. The credentials of a server on a Unix socket return
// it, so that the audit can say who made each call.
type UnixPeer struct {
	credentials.CommonAuthInfo

	UID int
	PID int
}

// AuthType returns "unix".
func (UnixPeer) AuthType() string { return "unix" }

// UnaryInterceptor audits unary calls.
func (a Audit) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		resp, err := handler(ctx, req)
		a.record(ctx, info.FullMethod, resourceOf(req), started, err)

		return resp, err
	}
}

// StreamInterceptor audits streaming calls, when they end. The resource is
// the one the stream's first message names.
func (a Audit) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		started := time.Now()
		audited := &auditedStream{ServerStream: ss}
		err := handler(srv, audited)
		a.record(ss.Context(), info.FullMethod, audited.resource(), started, err)

		return err
	}
}

// auditedStream is a stream that remembers the resource its first message
// names.
type auditedStream struct {
	grpc.ServerStream

	// A handler may read from a goroutine of its own, such as one copying
	// stdin.
	mu       sync.Mutex
	received bool
	named    string
}

// RecvMsg receives a message, remembering the resource it names if it is
// the first.
func (s *auditedStream) RecvMsg(m any) error {
	err := s.ServerStream.RecvMsg(m)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.received {
		s.received = true
		s.named = resourceOf(m)
	}
	return nil
}

// resource returns the resource the first message named, or "" if there was
// none.
func (s *auditedStream) resource() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.named
}

// record logs one finished call, unless it could not have changed anything.
func (a Audit) record(ctx context.Context, fullMethod, resource string, started time.Time, err error) {
	method := path.Base(fullMethod)
	if isRead(method) {
		return
	}

	attrs := []any{"method", method}
	if resource != "" {
		attrs = append(attrs, "resource", resource)
	}
	attrs = append(attrs, callerAttrs(ctx)...)
	attrs = append(attrs,
		"code", status.Code(err).String(),
		"duration", time.Since(started),
	)
	a.logger.InfoContext(ctx, "api call", attrs...)
}

// isRead reports whether a method is a Get or List.
func isRead(method string) bool {
	return strings.HasPrefix(method, "Get") || strings.HasPrefix(method, "List")
}

// resourceOf returns the name of the resource a request names: an
// instance, snapshot, network, volume or kernel by its name, an image by its
// reference. It returns "" for a request that names none, such as a prune.
func resourceOf(req any) string {
	switch r := req.(type) {
	case *dicerdv1.ExecInstanceRequest:
		return r.GetStart().GetName()
	case *dicerdv1.CopyToInstanceRequest:
		return r.GetStart().GetName()
	case *dicerdv1.CreateSnapshotRequest:
		// A snapshot not given a name is only named once it is taken, so
		// it is known by its instance.
		if r.GetName() == "" {
			return r.GetInstance()
		}
		return r.GetName()
	case interface{ GetName() string }:
		return r.GetName()
	case interface{ GetRef() string }:
		return r.GetRef()
	}
	return ""
}

// callerAttrs returns the log attributes that say who made a call: the user
// and process on the Unix socket, or the address and the token's name on
// TCP.
func callerAttrs(ctx context.Context) []any {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}

	switch info := p.AuthInfo.(type) {
	case UnixPeer:
		return []any{"uid", info.UID, "pid", info.PID}
	case credentials.TLSInfo:
		attrs := []any{"address", p.Addr.String()}
		if t, ok := tokenFrom(ctx); ok {
			attrs = append(attrs, "token", t.Name)
		}
		return attrs
	}
	if p.Addr != nil && p.Addr.Network() != "unix" {
		return []any{"address", p.Addr.String()}
	}
	return nil
}
