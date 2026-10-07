// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// TestLogsReadsTheLogThenEnds checks that a log is read as one stream, and
// that a failure arrives as a read error.
func TestLogsReadsTheLogThenEnds(t *testing.T) {
	c := connect(t, &fakeDaemon{
		getInstanceLogs: func(
			req *dicerdv1.GetInstanceLogsRequest, stream grpc.ServerStreamingServer[dicerdv1.InstanceLogChunk],
		) error {
			if req.GetName() != "web" {
				return status.Error(codes.NotFound, "no instance "+req.GetName())
			}
			for _, line := range []string{"booting\n", "ready\n"} {
				if err := stream.Send(&dicerdv1.InstanceLogChunk{Data: []byte(line)}); err != nil {
					return err
				}
			}
			return nil
		},
	})

	r, err := c.Instances.Logs(t.Context(), "web", LogOptions{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "booting\nready\n" {
		t.Errorf("read %q, %v", data, err)
	}

	r, err = c.Instances.Logs(t.Context(), "db", LogOptions{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer func() { _ = r.Close() }()
	if _, err := io.ReadAll(r); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a missing instance's log = %v, want ErrNotFound", err)
	}
}
