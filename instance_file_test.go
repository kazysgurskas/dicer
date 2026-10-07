// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/archive"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// guestFiles returns a daemon whose instance's filesystem is a directory on
// this machine, which copies are made into and out of as the guest agent
// makes them.
func guestFiles(t *testing.T) (*fakeDaemon, string) {
	t.Helper()
	root := t.TempDir()

	return &fakeDaemon{
		copyToInstance: func(stream grpc.ClientStreamingServer[dicerdv1.CopyToInstanceRequest, emptypb.Empty]) error {
			first, err := stream.Recv()
			if err != nil {
				return err
			}
			receive := func() ([]byte, error) {
				req, err := stream.Recv()
				return req.GetData(), err
			}
			if err := archive.Receive(receive, filepath.Join(root, first.GetStart().GetPath())); err != nil {
				return status.Error(codes.InvalidArgument, err.Error())
			}
			return stream.SendAndClose(&emptypb.Empty{})
		},
		copyFromInstance: func(
			req *dicerdv1.CopyFromInstanceRequest, stream grpc.ServerStreamingServer[dicerdv1.CopyFromInstanceResponse],
		) error {
			src := filepath.Join(root, req.GetPath())
			if _, err := os.Stat(src); err != nil {
				return status.Errorf(codes.NotFound, "%s: no such file or directory", req.GetPath())
			}
			_, err := archive.Send(src, func(chunk []byte) error {
				return stream.Send(&dicerdv1.CopyFromInstanceResponse{Data: chunk})
			})
			return err
		},
	}, root
}

// TestWriteFileThenReadFile checks that a file written into an instance
// reads back as it was written, with its mode.
func TestWriteFileThenReadFile(t *testing.T) {
	daemon, root := guestFiles(t)
	c := connect(t, daemon)

	if err := c.Instances.WriteFile(t.Context(), "web", "/job.py", []byte("print(1)"), 0o750); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "job.py"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("written with mode %v, want 0750", info.Mode().Perm())
	}

	data, err := c.Instances.ReadFile(t.Context(), "web", "/job.py")
	if err != nil || string(data) != "print(1)" {
		t.Errorf("ReadFile = %q, %v; want print(1)", data, err)
	}
}

// TestReadFileFailures checks that a file that cannot be read is reported
// as the guest said, rather than as a broken archive.
func TestReadFileFailures(t *testing.T) {
	daemon, root := guestFiles(t)
	c := connect(t, daemon)
	if err := os.Mkdir(filepath.Join(root, "srv"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Instances.ReadFile(t.Context(), "web", "/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadFile of a missing file = %v, want ErrNotFound", err)
	}
	if _, err := c.Instances.ReadFile(t.Context(), "web", "/srv"); err == nil {
		t.Error("ReadFile of a directory succeeded")
	}
}

// TestCopyToThenCopyFrom checks that a directory copied into an instance and
// out again arrives whole.
func TestCopyToThenCopyFrom(t *testing.T) {
	daemon, _ := guestFiles(t)
	c := connect(t, daemon)

	src := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "main.py"), []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := c.Instances.CopyTo(t.Context(), "web", src, "/"); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	dest := t.TempDir()
	if err := c.Instances.CopyFrom(t.Context(), "web", "/app", dest); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dest, "app", "lib", "main.py"))
	if err != nil || string(data) != "main" {
		t.Errorf("copied back %q, %v; want main", data, err)
	}
}

// TestCopyFromAMissingPathIsNotFound checks that the guest's own failure is
// what CopyFrom reports.
func TestCopyFromAMissingPathIsNotFound(t *testing.T) {
	daemon, _ := guestFiles(t)
	c := connect(t, daemon)

	if err := c.Instances.CopyFrom(t.Context(), "web", "/missing", t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Errorf("CopyFrom = %v, want ErrNotFound", err)
	}
}

// TestCopyToAMissingSourceFailsLocally checks that a source that cannot be
// packed is reported as this machine's failure.
func TestCopyToAMissingSourceFailsLocally(t *testing.T) {
	daemon, _ := guestFiles(t)
	c := connect(t, daemon)

	err := c.Instances.CopyTo(t.Context(), "web", filepath.Join(t.TempDir(), "missing"), "/")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CopyTo = %v, want fs.ErrNotExist", err)
	}
}
