// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// importKernelStream is an ImportKernel stream that sends messages and keeps
// the response.
type importKernelStream struct {
	grpc.ServerStream

	ctx      context.Context
	messages []*dicerdv1.ImportKernelRequest
	resp     *dicerdv1.Kernel
}

func (s *importKernelStream) Context() context.Context { return s.ctx }

func (s *importKernelStream) Recv() (*dicerdv1.ImportKernelRequest, error) {
	if len(s.messages) == 0 {
		return nil, io.EOF
	}
	m := s.messages[0]
	s.messages = s.messages[1:]
	return m, nil
}

func (s *importKernelStream) SendAndClose(k *dicerdv1.Kernel) error {
	s.resp = k
	return nil
}

// importKernel imports the kernel start describes, with the chunks a client
// sends after it, and returns the kernel recorded.
func importKernel(
	t *testing.T, s *Server, start *dicerdv1.ImportKernelStart, chunks ...string,
) (*dicerdv1.Kernel, error) {
	t.Helper()

	messages := []*dicerdv1.ImportKernelRequest{{Payload: &dicerdv1.ImportKernelRequest_Start{Start: start}}}
	for _, c := range chunks {
		messages = append(messages, &dicerdv1.ImportKernelRequest{Payload: &dicerdv1.ImportKernelRequest_Data{Data: []byte(c)}})
	}
	stream := &importKernelStream{ctx: t.Context(), messages: messages}
	err := s.ImportKernel(stream)
	return stream.resp, err
}

// x86Kernel returns the start of an import of an x86_64 kernel.
func x86Kernel(name, sha256 string) *dicerdv1.ImportKernelStart {
	return &dicerdv1.ImportKernelStart{Name: name, Arch: dicerdv1.Architecture_ARCHITECTURE_X86_64, Sha256: sha256}
}

// A kernel an instance boots cannot be deleted; once nothing boots it, it
// can.
func TestDeleteKernelRefusesOneInUse(t *testing.T) {
	s, store := newTestServer(t)
	if _, err := importKernel(t, s, x86Kernel("k", ""), "vmlinux"); err != nil {
		t.Fatalf("ImportKernel: %v", err)
	}
	if err := store.CreateInstance(instance.Spec{ID: "i-1", Name: "web", KernelName: "k", NetworkName: "default"}); err != nil {
		t.Fatal(err)
	}

	_, err := s.DeleteKernel(t.Context(), &dicerdv1.DeleteKernelRequest{Name: "k"})
	wantClass(t, err, errdefs.ErrInvalidState)

	if err := store.DeleteInstance("web"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteKernel(t.Context(), &dicerdv1.DeleteKernelRequest{Name: "k"}); err != nil {
		t.Fatalf("DeleteKernel: %v", err)
	}
}

// TestImportKernelKeepsAKernelTheClientSends checks that a kernel the client
// sends in pieces is recorded with the SHA-256 of what was sent, and is on the
// host.
func TestImportKernelKeepsAKernelTheClientSends(t *testing.T) {
	s, store := newTestServer(t)

	const contents = "a sent kernel"
	sum := sha256.Sum256([]byte(contents))
	digest := hex.EncodeToString(sum[:])

	k, err := importKernel(t, s, x86Kernel("sent", ""), contents[:5], contents[5:])
	if err != nil {
		t.Fatalf("ImportKernel: %v", err)
	}
	if k.GetSha256() != digest {
		t.Errorf("kernel = %+v, want the SHA-256 %s", k, digest)
	}

	stored, err := store.Kernel("sent")
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.kernelManager.Path(stored)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != contents {
		t.Errorf("the kernel kept = %q, %v; want %q", data, err, contents)
	}
}

func TestImportKernelRefusesWhatItCannotKeep(t *testing.T) {
	s, store := newTestServer(t)
	if _, err := importKernel(t, s, x86Kernel("taken", ""), "vmlinux"); err != nil {
		t.Fatal(err)
	}

	noStart := &importKernelStream{ctx: t.Context(), messages: []*dicerdv1.ImportKernelRequest{
		{Payload: &dicerdv1.ImportKernelRequest_Data{Data: []byte("x")}},
	}}
	if err := s.ImportKernel(noStart); err == nil {
		t.Error("an import with no start message succeeded")
	}

	for _, tt := range []struct {
		name  string
		start *dicerdv1.ImportKernelStart
	}{
		{"a name another kernel has", x86Kernel("taken", "")},
		{"the default kernel's name", x86Kernel(kernel.DefaultName, "")},
		{"a checksum it fails", x86Kernel("bad", strings.Repeat("00", 32))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := importKernel(t, s, tt.start, "kernel"); err == nil {
				t.Fatal("ImportKernel succeeded")
			}
		})
	}
	if _, err := store.Kernel("bad"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("a kernel that failed its checksum was recorded: %v", err)
	}

	if _, err := importKernel(t, s, x86Kernel("empty", "")); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("importing an empty kernel = %v, want errdefs.ErrInvalidArgument", err)
	}
	if _, err := store.Kernel("empty"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("an empty kernel was recorded: %v", err)
	}
}

// TestDefaultKernelIsReserved checks that the default kernel cannot be
// deleted, and that no kernel can be imported under its name.
func TestDefaultKernelIsReserved(t *testing.T) {
	s, _ := newTestServer(t)

	_, err := s.DeleteKernel(t.Context(), &dicerdv1.DeleteKernelRequest{Name: kernel.DefaultName})
	wantClass(t, err, errdefs.ErrInvalidArgument)

	_, err = importKernel(t, s, x86Kernel(kernel.DefaultName, ""), "vmlinux")
	wantClass(t, err, errdefs.ErrInvalidArgument)
}
