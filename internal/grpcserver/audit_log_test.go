// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/token"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// auditLogTo returns an audit log that writes to a buffer, and the buffer.
func auditLogTo() (auditLog, *bytes.Buffer) {
	var buf bytes.Buffer
	return newAuditLog(slog.New(slog.NewTextHandler(&buf, nil))), &buf
}

// callUnary audits a unary call of method with req, made by caller with the
// named token, or with none if it is empty.
func callUnary(t *testing.T, a auditLog, caller *peer.Peer, tokenName, method string, req any) {
	t.Helper()

	ctx := peer.NewContext(t.Context(), caller)
	if tokenName != "" {
		ctx = context.WithValue(ctx, tokenKey{}, token.Token{Name: tokenName})
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/dicerd.v1.DaemonService/" + method}
	handler := func(context.Context, any) (any, error) { return &emptypb.Empty{}, nil }
	if _, err := a.unaryInterceptor()(ctx, req, info, handler); err != nil {
		t.Fatal(err)
	}
}

func TestAuditNamesTheCallerAndTheResource(t *testing.T) {
	unixAddr := &net.UnixAddr{Name: "@", Net: "unix"}
	tcpAddr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 51234}

	tests := []struct {
		name   string
		caller *peer.Peer
		token  string
		want   string
	}{
		{
			"unix socket",
			&peer.Peer{Addr: unixAddr, AuthInfo: unixPeer{UID: 1000, PID: 4242}},
			"",
			"resource=web uid=1000 pid=4242 code=OK",
		},
		{
			"tcp",
			&peer.Peer{Addr: tcpAddr},
			"",
			"resource=web address=10.0.0.5:51234 code=OK",
		},
		{
			"tls with a token",
			&peer.Peer{Addr: tcpAddr, AuthInfo: credentials.TLSInfo{}},
			"ci",
			"resource=web address=10.0.0.5:51234 token=ci code=OK",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, buf := auditLogTo()
			callUnary(t, a, tt.caller, tt.token, "StopInstance", &dicerdv1.StopInstanceRequest{Name: "web"})

			if !strings.Contains(buf.String(), "method=StopInstance "+tt.want) {
				t.Errorf("audit = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

func TestAuditLeavesOutReads(t *testing.T) {
	a, buf := auditLogTo()
	caller := &peer.Peer{Addr: &net.UnixAddr{Name: "@", Net: "unix"}, AuthInfo: unixPeer{UID: 0, PID: 1}}
	callUnary(t, a, caller, "", "GetInstance", &dicerdv1.GetInstanceRequest{Name: "web"})

	if buf.Len() != 0 {
		t.Errorf("audit = %q, want nothing for a read", buf.String())
	}
}

// TestAuditNamesTheResourceAStreamStartsWith checks that a stream is audited
// with the resource its first message names, as exec's does.
func TestAuditNamesTheResourceAStreamStartsWith(t *testing.T) {
	a, buf := auditLogTo()
	ss := &fakeServerStream{
		ctx: peer.NewContext(t.Context(), &peer.Peer{AuthInfo: unixPeer{UID: 1000, PID: 4242}}),
		messages: []proto.Message{
			&dicerdv1.ExecInstanceRequest{Payload: &dicerdv1.ExecInstanceRequest_Start{
				Start: &dicerdv1.ExecInstanceStart{Name: "web", Command: []string{"sh"}},
			}},
			&dicerdv1.ExecInstanceRequest{Payload: &dicerdv1.ExecInstanceRequest_Stdin{Stdin: []byte("ls\n")}},
		},
	}
	handler := func(_ any, stream grpc.ServerStream) error {
		for range ss.messages {
			if err := stream.RecvMsg(&dicerdv1.ExecInstanceRequest{}); err != nil {
				return err
			}
		}
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: "/dicerd.v1.DaemonService/ExecInstance"}
	if err := a.streamInterceptor()(nil, ss, info, handler); err != nil {
		t.Fatal(err)
	}

	if want := "method=ExecInstance resource=web uid=1000 pid=4242"; !strings.Contains(buf.String(), want) {
		t.Errorf("audit = %q, want %q", buf.String(), want)
	}
}

func TestResourceOf(t *testing.T) {
	tests := []struct {
		name string
		req  any
		want string
	}{
		{"by name", &dicerdv1.DeleteVolumeRequest{Name: "data"}, "data"},
		{"an image by reference", &dicerdv1.PullImageRequest{Ref: "nginx:1.27"}, "nginx:1.27"},
		{"copy, by its start", &dicerdv1.CopyToInstanceRequest{Payload: &dicerdv1.CopyToInstanceRequest_Start{
			Start: &dicerdv1.CopyToInstanceStart{Name: "web", Path: "/tmp"},
		}}, "web"},
		{"a named snapshot", &dicerdv1.CreateSnapshotRequest{Instance: "web", Name: "before"}, "before"},
		{"an unnamed snapshot, by its instance", &dicerdv1.CreateSnapshotRequest{Instance: "web"}, "web"},
		{"nothing named", &dicerdv1.PruneImagesRequest{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resourceOf(tt.req); got != tt.want {
				t.Errorf("resourceOf = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeServerStream is a server stream that receives messages in turn.
type fakeServerStream struct {
	grpc.ServerStream

	ctx      context.Context
	messages []proto.Message
	next     int
}

func (s *fakeServerStream) Context() context.Context { return s.ctx }

func (s *fakeServerStream) RecvMsg(m any) error {
	msg, ok := m.(proto.Message)
	if !ok {
		return fmt.Errorf("receive into a %T", m)
	}
	proto.Merge(msg, s.messages[s.next])
	s.next++
	return nil
}
