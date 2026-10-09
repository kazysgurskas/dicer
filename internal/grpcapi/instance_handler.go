// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/nrednav/cuid2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/filestore"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/image/reference"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// instanceHandler handles instance-related RPCs.
type instanceHandler struct {
	store     *filestore.Store
	instances *instance.Manager

	// statsInterval is how often GetInstanceStats reads stats.
	statsInterval time.Duration
}

// CreateInstance records an instance definition, pulling its image as the
// request's pull policy says. It does not boot anything unless the request
// asks for it.
func (h *instanceHandler) CreateInstance(
	ctx context.Context, req *dicerdv1.CreateInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.newInstance(req)
	if err != nil {
		return nil, err
	}
	pull, err := pullPolicies.fromProto(req.GetPullPolicy())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Create(ctx, instance, pull); err != nil {
		return nil, err
	}

	if req.GetStart() {
		if err := h.instances.Start(ctx, instance); err != nil {
			return nil, fmt.Errorf("instance %q was created, but did not start: %w", instance.Name, err)
		}
	}

	return h.view(instance)
}

// newInstance validates a create request and returns the instance it
// defines. A kernel or network left out is the default one.
func (h *instanceHandler) newInstance(req *dicerdv1.CreateInstanceRequest) (instance.Spec, error) {
	req.KernelName = cmp.Or(req.GetKernelName(), kernel.DefaultName)
	req.NetworkName = cmp.Or(req.GetNetworkName(), network.DefaultName)

	ports, err := portMappingsFromProto(req.GetPorts())
	if err != nil {
		return instance.Spec{}, err
	}
	mounts, err := mountsFromProto(req.GetMounts())
	if err != nil {
		return instance.Spec{}, err
	}
	restart, err := restartPolicyFromProto(req.GetRestartPolicy())
	if err != nil {
		return instance.Spec{}, err
	}
	healthCheck, err := healthCheckFromProto(req.GetHealthCheck())
	if err != nil {
		return instance.Spec{}, err
	}
	initMode, err := initModes.fromProto(req.GetInitMode())
	if err != nil {
		return instance.Spec{}, err
	}
	hypervisorType, err := hypervisorTypes.fromProto(req.GetHypervisorType())
	if err != nil {
		return instance.Spec{}, err
	}

	now := time.Now()
	spec := instance.Spec{
		ID:                     cuid2.Generate(),
		Name:                   req.GetName(),
		Hostname:               req.GetHostname(),
		ImageRef:               req.GetImageRef(),
		HypervisorType:         hypervisorType,
		HypervisorVersion:      req.GetHypervisorVersion(),
		KernelName:             req.GetKernelName(),
		KernelArgs:             req.GetKernelArgs(),
		VCPUs:                  int(req.GetVcpus()),
		MemoryBytes:            req.GetMemoryBytes(),
		MaxVCPUs:               int(req.GetMaxVcpus()),
		MaxMemoryBytes:         req.GetMaxMemoryBytes(),
		DiskBytes:              req.GetDiskBytes(),
		DiskBytesPerSecond:     req.GetDiskBytesPerSecond(),
		DiskIOPS:               req.GetDiskIops(),
		UploadBytesPerSecond:   req.GetUploadBytesPerSecond(),
		DownloadBytesPerSecond: req.GetDownloadBytesPerSecond(),
		StandbyAfter:           req.GetStandbyAfter().AsDuration(),
		NetworkName:            req.GetNetworkName(),
		StaticIP:               req.GetStaticIp(),
		Ports:                  ports,
		Mounts:                 mounts,
		Env:                    req.GetEnv(),
		Cmd:                    req.GetCmd(),
		Labels:                 req.GetLabels(),
		Restart:                restart,
		HealthCheck:            healthCheck,
		InitMode:               cmp.Or(initMode, guest.InitModeAuto),
		RemoveOnExit:           req.GetRemoveOnExit(),
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := spec.Validate(); err != nil {
		return instance.Spec{}, err
	}
	imageRef, err := reference.Parse(spec.ImageRef)
	if err != nil {
		return instance.Spec{}, errdefs.InvalidArgument("invalid image %q: %v", spec.ImageRef, err)
	}
	spec.ImageRef = imageRef.String()

	if _, err := h.store.Instance(spec.Name); err == nil {
		return instance.Spec{}, errdefs.Exists("instance %q already exists", spec.Name)
	}
	if err := h.checkCanStart(spec); err != nil {
		return instance.Spec{}, err
	}

	return spec, nil
}

// UpdateInstance modifies an instance's definition. See
// instance.Manager.Update.
func (h *instanceHandler) UpdateInstance(
	ctx context.Context, req *dicerdv1.UpdateInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.applyReferences(&instance, req); err != nil {
		return nil, err
	}
	applySettings(&instance, req)
	if err := applyLists(&instance, req); err != nil {
		return nil, err
	}
	if p := req.GetRestartPolicy(); p != nil {
		if instance.Restart, err = restartPolicyFromProto(p); err != nil {
			return nil, err
		}
	}
	if c := req.GetHealthCheck(); c != nil {
		if instance.HealthCheck, err = healthCheckFromProto(c); err != nil {
			return nil, err
		}
	}
	if m := req.GetInitMode(); m != dicerdv1.InitMode_INIT_MODE_UNSPECIFIED {
		if instance.InitMode, err = initModes.fromProto(m); err != nil {
			return nil, err
		}
	}
	if t := req.GetHypervisorType(); t != dicerdv1.HypervisorType_HYPERVISOR_TYPE_UNSPECIFIED {
		if instance.HypervisorType, err = hypervisorTypes.fromProto(t); err != nil {
			return nil, err
		}
	}

	if err := instance.Validate(); err != nil {
		return nil, err
	}
	if err := h.checkCanStart(instance); err != nil {
		return nil, err
	}

	instance.UpdatedAt = time.Now()
	if err := h.instances.Update(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// applyReferences applies the image, kernel and network an update names,
// checking that each is valid or exists.
func (h *instanceHandler) applyReferences(instance *instance.Spec, req *dicerdv1.UpdateInstanceRequest) error {
	if v := req.ImageRef; v != nil {
		ref, err := reference.Parse(*v)
		if err != nil {
			return errdefs.InvalidArgument("invalid image %q: %v", *v, err)
		}
		instance.ImageRef = ref.String()
	}
	if v := req.KernelName; v != nil {
		if _, err := h.store.Kernel(*v); err != nil {
			return errdefs.InvalidArgument("%v", err)
		}
		instance.KernelName = *v
	}
	if v := req.NetworkName; v != nil {
		if _, err := h.store.Network(*v); err != nil {
			return errdefs.InvalidArgument("%v", err)
		}
		instance.NetworkName = *v
	}
	return nil
}

// applySettings applies the scalar fields an update sets.
func applySettings(instance *instance.Spec, req *dicerdv1.UpdateInstanceRequest) {
	if v := req.Vcpus; v != nil {
		instance.VCPUs = int(*v)
	}
	setIf(&instance.HypervisorVersion, req.HypervisorVersion)
	setIf(&instance.KernelArgs, req.KernelArgs)
	setIf(&instance.MemoryBytes, req.MemoryBytes)
	if v := req.MaxVcpus; v != nil {
		instance.MaxVCPUs = int(*v)
	}
	setIf(&instance.MaxMemoryBytes, req.MaxMemoryBytes)
	setIf(&instance.DiskBytes, req.DiskBytes)
	setIf(&instance.DiskBytesPerSecond, req.DiskBytesPerSecond)
	setIf(&instance.DiskIOPS, req.DiskIops)
	setIf(&instance.UploadBytesPerSecond, req.UploadBytesPerSecond)
	setIf(&instance.DownloadBytesPerSecond, req.DownloadBytesPerSecond)
	if d := req.GetStandbyAfter(); d != nil {
		instance.StandbyAfter = d.AsDuration()
	}
	setIf(&instance.StaticIP, req.StaticIp)
	setIf(&instance.Hostname, req.Hostname)
	setIf(&instance.RemoveOnExit, req.RemoveOnExit)
}

// setIf sets *dst to *v if v is set.
func setIf[T any](dst, v *T) {
	if v != nil {
		*dst = *v
	}
}

// applyLists replaces each list or map an update gives a non-empty value.
func applyLists(instance *instance.Spec, req *dicerdv1.UpdateInstanceRequest) error {
	if len(req.GetMounts()) > 0 {
		mounts, err := mountsFromProto(req.GetMounts())
		if err != nil {
			return err
		}
		instance.Mounts = mounts
	}
	if len(req.GetPorts()) > 0 {
		ports, err := portMappingsFromProto(req.GetPorts())
		if err != nil {
			return err
		}
		instance.Ports = ports
	}
	if len(req.GetEnv()) > 0 {
		instance.Env = req.GetEnv()
	}
	if len(req.GetCmd()) > 0 {
		instance.Cmd = req.GetCmd()
	}
	if len(req.GetLabels()) > 0 {
		instance.Labels = req.GetLabels()
	}
	return nil
}

// StartInstance boots an instance.
func (h *instanceHandler) StartInstance(
	ctx context.Context, req *dicerdv1.StartInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Start(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// StopInstance shuts an instance down.
func (h *instanceHandler) StopInstance(
	ctx context.Context, req *dicerdv1.StopInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Stop(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// PauseInstance pauses a running instance.
func (h *instanceHandler) PauseInstance(
	ctx context.Context, req *dicerdv1.PauseInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Pause(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// StandbyInstance freezes a running or paused instance to disk. See
// instance.Manager.Standby.
func (h *instanceHandler) StandbyInstance(
	ctx context.Context, req *dicerdv1.StandbyInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Standby(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// ResizeInstance changes a running instance's vCPUs and memory. See
// instance.Manager.Resize.
func (h *instanceHandler) ResizeInstance(
	ctx context.Context, req *dicerdv1.ResizeInstanceRequest,
) (*dicerdv1.Instance, error) {
	if req.Vcpus == nil && req.MemoryBytes == nil {
		return nil, errdefs.InvalidArgument("a resize needs vcpus, memory_bytes or both")
	}
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	want := instance.Resources()
	if v := req.Vcpus; v != nil {
		want.VCPUs = int(*v)
	}
	setIf(&want.MemoryBytes, req.MemoryBytes)
	switch {
	case want.VCPUs <= 0:
		return nil, errdefs.InvalidArgument("an instance needs at least 1 vCPU")
	case want.MemoryBytes <= 0:
		return nil, errdefs.InvalidArgument("an instance needs more than 0 bytes of memory")
	}

	if err := h.instances.Resize(ctx, instance, want); err != nil {
		return nil, err
	}

	instance.VCPUs, instance.MemoryBytes = want.VCPUs, want.MemoryBytes
	return h.view(instance)
}

// ResumeInstance resumes a paused instance.
func (h *instanceHandler) ResumeInstance(
	ctx context.Context, req *dicerdv1.ResumeInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Resume(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// RenameInstance changes a stopped instance's name.
func (h *instanceHandler) RenameInstance(
	ctx context.Context, req *dicerdv1.RenameInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	renamed, err := h.instances.Rename(ctx, instance, req.GetNewName())
	if err != nil {
		return nil, err
	}

	return h.view(renamed)
}

// DeleteInstance removes an instance and everything it owns but its
// volumes. An active instance is refused unless the request forces it.
func (h *instanceHandler) DeleteInstance(
	ctx context.Context, req *dicerdv1.DeleteInstanceRequest,
) (*emptypb.Empty, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	if err := h.instances.Delete(ctx, instance, req.GetForce()); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// GetInstance returns an instance with its status.
func (h *instanceHandler) GetInstance(
	_ context.Context, req *dicerdv1.GetInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.store.Instance(req.GetName())
	if err != nil {
		return nil, err
	}

	return h.view(instance)
}

// ListInstances lists every instance with its status, sorted by name.
func (h *instanceHandler) ListInstances(
	_ context.Context, _ *dicerdv1.ListInstancesRequest,
) (*dicerdv1.ListInstancesResponse, error) {
	instances := h.store.Instances()

	resp := &dicerdv1.ListInstancesResponse{
		Instances: make([]*dicerdv1.Instance, 0, len(instances)),
	}
	for _, instance := range instances {
		view, err := h.view(instance)
		if err != nil {
			return nil, err
		}
		resp.Instances = append(resp.Instances, view)
	}

	return resp, nil
}

// view assembles the API representation of an instance.
func (h *instanceHandler) view(instance instance.Spec) (*dicerdv1.Instance, error) {
	return viewInstance(h.instances, instance)
}

// viewInstance assembles an instance's spec, status, address and health.
func viewInstance(instances *instance.Manager, spec instance.Spec) (*dicerdv1.Instance, error) {
	status, err := instances.Status(spec)
	if err != nil {
		return nil, err
	}

	if allocation, err := instances.Allocation(spec); err == nil {
		status.IP, status.MAC = allocation.IP, allocation.MAC
	}
	if check, health, ok := instances.Health(spec); ok {
		status.HealthCheck, status.Health = &check, &health
	}

	return instanceToProto(instance.Instance{Spec: spec, Status: status}), nil
}
