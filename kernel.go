// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// Kernels are the calls about guest kernels, reached as Client.Kernels.
type Kernels struct {
	api dicerdv1.DaemonServiceClient
}

// Kernel is a guest kernel instances can boot.
type Kernel struct {
	ID string `json:"id,omitzero"`

	KernelSpec

	CreateTime time.Time `json:"create_time,omitzero"`
	UpdateTime time.Time `json:"update_time,omitzero"`
}

// KernelSpec is where a kernel is downloaded from, and what it is.
type KernelSpec struct {
	Name         string       `json:"name,omitzero"`
	URL          string       `json:"url,omitzero"`
	Architecture Architecture `json:"architecture,omitzero"`

	// SHA256 is the expected SHA-256 of the download, hex-encoded. It is
	// verified when set.
	SHA256 string `json:"sha256,omitzero"`
}

// Architecture is the CPU architecture a kernel is built for.
type Architecture string

// The architectures.
const (
	ArchitectureX86_64  Architecture = "x86_64"
	ArchitectureAArch64 Architecture = "aarch64"
)

var architectures = enum[Architecture, dicerdv1.Architecture]{"architecture", map[Architecture]dicerdv1.Architecture{
	ArchitectureX86_64:  dicerdv1.Architecture_ARCHITECTURE_X86_64,
	ArchitectureAArch64: dicerdv1.Architecture_ARCHITECTURE_AARCH64,
}}

// Import records a kernel by URL. It is downloaded the first time an
// instance boots with it.
func (s *Kernels) Import(ctx context.Context, spec KernelSpec) (Kernel, error) {
	req, err := importKernelRequest(spec)
	if err != nil {
		return Kernel{}, err
	}
	resp, err := s.api.ImportKernel(ctx, req)
	if err != nil {
		return Kernel{}, fromStatus(err)
	}
	return kernelFromProto(resp), nil
}

// List returns every kernel, the default one among them.
func (s *Kernels) List(ctx context.Context) ([]Kernel, error) {
	resp, err := s.api.ListKernels(ctx, &dicerdv1.ListKernelsRequest{})
	if err != nil {
		return nil, fromStatus(err)
	}
	return convertAll(resp.GetKernels(), kernelFromProto), nil
}

// Get returns one kernel, or ErrNotFound.
func (s *Kernels) Get(ctx context.Context, name string) (Kernel, error) {
	resp, err := s.api.GetKernel(ctx, &dicerdv1.GetKernelRequest{Name: name})
	if err != nil {
		return Kernel{}, fromStatus(err)
	}
	return kernelFromProto(resp), nil
}

// Delete removes a kernel that no instance references. The default kernel
// cannot be deleted.
func (s *Kernels) Delete(ctx context.Context, name string) error {
	_, err := s.api.DeleteKernel(ctx, &dicerdv1.DeleteKernelRequest{Name: name})
	return fromStatus(err)
}

// importKernelRequest returns the request that imports the kernel spec
// describes.
func importKernelRequest(spec KernelSpec) (*dicerdv1.ImportKernelRequest, error) {
	arch, err := architectures.toProto(spec.Architecture)
	if err != nil {
		return nil, err
	}

	return &dicerdv1.ImportKernelRequest{Name: spec.Name, Url: spec.URL, Arch: arch, Sha256: spec.SHA256}, nil
}

// kernelFromProto returns the kernel p describes.
func kernelFromProto(p *dicerdv1.Kernel) Kernel {
	return Kernel{
		ID: p.GetId(),
		KernelSpec: KernelSpec{
			Name:         p.GetName(),
			URL:          p.GetUrl(),
			Architecture: architectures.fromProto(p.GetArch()),
			SHA256:       p.GetSha256(),
		},
		CreateTime: timeFromProto(p.GetCreateTime()),
		UpdateTime: timeFromProto(p.GetUpdateTime()),
	}
}
