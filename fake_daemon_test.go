// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// fakeDaemon answers the calls a test gives it a function for, and fails
// the others as unimplemented.
type fakeDaemon struct {
	dicerdv1.UnimplementedDaemonServiceServer

	// fingerprint is the fingerprint of the certificate serve gave it.
	fingerprint string

	getInstance      func(context.Context, *dicerdv1.GetInstanceRequest) (*dicerdv1.Instance, error)
	getInstanceLogs  func(*dicerdv1.GetInstanceLogsRequest, grpc.ServerStreamingServer[dicerdv1.InstanceLogChunk]) error
	execInstance     func(grpc.BidiStreamingServer[dicerdv1.ExecInstanceRequest, dicerdv1.ExecInstanceResponse]) error
	copyToInstance   func(grpc.ClientStreamingServer[dicerdv1.CopyToInstanceRequest, emptypb.Empty]) error
	copyFromInstance func(*dicerdv1.CopyFromInstanceRequest, grpc.ServerStreamingServer[dicerdv1.CopyFromInstanceResponse]) error
	pullImage        func(*dicerdv1.PullImageRequest, grpc.ServerStreamingServer[dicerdv1.PullImageProgress]) error
	getEvents        func(*dicerdv1.GetEventsRequest, grpc.ServerStreamingServer[dicerdv1.GetEventsResponse]) error
	waitInstance     func(*dicerdv1.WaitInstanceRequest, grpc.ServerStreamingServer[dicerdv1.WaitInstanceResponse]) error
}

// connect returns a client of daemon, served over loopback until the test
// ends.
func connect(t *testing.T, daemon dicerdv1.DaemonServiceServer) *Client {
	t.Helper()

	address, value := serve(t, daemon)
	c, err := NewClient(WithAddress(address), WithToken(value))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return c
}

func (d *fakeDaemon) GetInstance(ctx context.Context, req *dicerdv1.GetInstanceRequest) (*dicerdv1.Instance, error) {
	if d.getInstance == nil {
		return d.UnimplementedDaemonServiceServer.GetInstance(ctx, req)
	}
	return d.getInstance(ctx, req)
}

func (d *fakeDaemon) GetInstanceLogs(
	req *dicerdv1.GetInstanceLogsRequest, stream grpc.ServerStreamingServer[dicerdv1.InstanceLogChunk],
) error {
	if d.getInstanceLogs == nil {
		return d.UnimplementedDaemonServiceServer.GetInstanceLogs(req, stream)
	}
	return d.getInstanceLogs(req, stream)
}

func (d *fakeDaemon) ExecInstance(
	stream grpc.BidiStreamingServer[dicerdv1.ExecInstanceRequest, dicerdv1.ExecInstanceResponse],
) error {
	if d.execInstance == nil {
		return d.UnimplementedDaemonServiceServer.ExecInstance(stream)
	}
	return d.execInstance(stream)
}

func (d *fakeDaemon) CopyToInstance(stream grpc.ClientStreamingServer[dicerdv1.CopyToInstanceRequest, emptypb.Empty]) error {
	if d.copyToInstance == nil {
		return d.UnimplementedDaemonServiceServer.CopyToInstance(stream)
	}
	return d.copyToInstance(stream)
}

func (d *fakeDaemon) CopyFromInstance(
	req *dicerdv1.CopyFromInstanceRequest, stream grpc.ServerStreamingServer[dicerdv1.CopyFromInstanceResponse],
) error {
	if d.copyFromInstance == nil {
		return d.UnimplementedDaemonServiceServer.CopyFromInstance(req, stream)
	}
	return d.copyFromInstance(req, stream)
}

func (d *fakeDaemon) PullImage(req *dicerdv1.PullImageRequest, stream grpc.ServerStreamingServer[dicerdv1.PullImageProgress]) error {
	if d.pullImage == nil {
		return d.UnimplementedDaemonServiceServer.PullImage(req, stream)
	}
	return d.pullImage(req, stream)
}

func (d *fakeDaemon) WaitInstance(
	req *dicerdv1.WaitInstanceRequest, stream grpc.ServerStreamingServer[dicerdv1.WaitInstanceResponse],
) error {
	if d.waitInstance == nil {
		return d.UnimplementedDaemonServiceServer.WaitInstance(req, stream)
	}
	return d.waitInstance(req, stream)
}

func (d *fakeDaemon) GetEvents(req *dicerdv1.GetEventsRequest, stream grpc.ServerStreamingServer[dicerdv1.GetEventsResponse]) error {
	if d.getEvents == nil {
		return d.UnimplementedDaemonServiceServer.GetEvents(req, stream)
	}
	return d.getEvents(req, stream)
}
