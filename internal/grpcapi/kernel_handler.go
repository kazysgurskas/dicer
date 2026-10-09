// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/kernel"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// kernelHandler handles kernel-related RPCs.
type kernelHandler struct {
	kernelManager *kernel.Manager
}

// ImportKernel puts a kernel the client sends on the host, and records it
// once it is there.
func (h *kernelHandler) ImportKernel(
	stream grpc.ClientStreamingServer[dicerdv1.ImportKernelRequest, dicerdv1.Kernel],
) error {
	req, err := stream.Recv()
	if err != nil {
		return errdefs.InvalidArgument("receive start message: %v", err)
	}
	start := req.GetStart()
	if start == nil {
		return errdefs.InvalidArgument("first message must be an ImportKernelStart")
	}
	arch, err := architectures.fromProto(start.GetArch())
	if err != nil {
		return err
	}

	k, err := h.kernelManager.Import(start.GetName(), arch, start.GetSha256(), &importKernelReader{stream: stream})
	if err != nil {
		return err
	}
	return stream.SendAndClose(kernelToProto(k))
}

// importKernelReader reads the kernel an ImportKernel stream carries after
// its start message.
type importKernelReader struct {
	stream grpc.ClientStreamingServer[dicerdv1.ImportKernelRequest, dicerdv1.Kernel]
	chunk  []byte
}

// Read reads the next of the kernel, and io.EOF once the client has sent it
// all.
func (r *importKernelReader) Read(p []byte) (int, error) {
	for len(r.chunk) == 0 {
		req, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		r.chunk = req.GetData()
	}

	n := copy(p, r.chunk)
	r.chunk = r.chunk[n:]
	return n, nil
}

// ListKernels lists the imported kernels, sorted by name.
func (h *kernelHandler) ListKernels(
	_ context.Context, _ *dicerdv1.ListKernelsRequest,
) (*dicerdv1.ListKernelsResponse, error) {
	kernels := h.kernelManager.Kernels()

	resp := &dicerdv1.ListKernelsResponse{
		Kernels: make([]*dicerdv1.Kernel, 0, len(kernels)),
	}
	for _, k := range kernels {
		resp.Kernels = append(resp.Kernels, kernelToProto(k))
	}

	return resp, nil
}

// GetKernel returns an imported kernel.
func (h *kernelHandler) GetKernel(
	_ context.Context, req *dicerdv1.GetKernelRequest,
) (*dicerdv1.Kernel, error) {
	k, err := h.kernelManager.Kernel(req.GetName())
	if err != nil {
		return nil, err
	}
	return kernelToProto(k), nil
}

// DeleteKernel removes a kernel and its copy on the host, refusing the default
// kernel and one an instance or snapshot boots.
func (h *kernelHandler) DeleteKernel(
	_ context.Context, req *dicerdv1.DeleteKernelRequest,
) (*emptypb.Empty, error) {
	if err := h.kernelManager.Delete(req.GetName()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
