// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/nrednav/cuid2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/guest"
	"github.com/konradasb/dicer/internal/image/reference"
	"github.com/konradasb/dicer/internal/instance"
	"github.com/konradasb/dicer/internal/kernel"
	"github.com/konradasb/dicer/internal/network"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// instanceHandler handles instance-related RPCs.
type instanceHandler struct {
	instanceManager *instance.Manager

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

	if err := h.instanceManager.Create(ctx, instance, pull); err != nil {
		return nil, err
	}

	if req.GetStart() {
		if err := h.instanceManager.Start(ctx, instance.ID); err != nil {
			return nil, fmt.Errorf("instance %q was created, but did not start: %w", instance.Name, err)
		}
	}

	return h.viewNamed(instance.ID)
}

// newInstance returns the instance a create request defines. A kernel or
// network left out is the default one.
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

	imageRef, err := reference.Parse(spec.ImageRef)
	if err != nil {
		return instance.Spec{}, errdefs.InvalidArgument("invalid image %q: %v", spec.ImageRef, err)
	}
	spec.ImageRef = imageRef.String()

	return spec, nil
}

// UpdateInstance modifies an instance's definition. See
// instance.Manager.Update.
func (h *instanceHandler) UpdateInstance(
	ctx context.Context, req *dicerdv1.UpdateInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := h.instanceManager.Instance(req.GetName())
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

	instance.UpdatedAt = time.Now()
	if err := h.instanceManager.Update(ctx, instance); err != nil {
		return nil, err
	}

	return h.view(instance)
}

// applyReferences applies the image, kernel and network an update names.
// The instance manager checks that the kernel and network exist.
func (h *instanceHandler) applyReferences(instance *instance.Spec, req *dicerdv1.UpdateInstanceRequest) error {
	if v := req.ImageRef; v != nil {
		ref, err := reference.Parse(*v)
		if err != nil {
			return errdefs.InvalidArgument("invalid image %q: %v", *v, err)
		}
		instance.ImageRef = ref.String()
	}
	setIf(&instance.KernelName, req.KernelName)
	setIf(&instance.NetworkName, req.NetworkName)
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
	if err := h.instanceManager.Start(ctx, req.GetName()); err != nil {
		return nil, err
	}
	return h.viewNamed(req.GetName())
}

// StopInstance shuts an instance down.
func (h *instanceHandler) StopInstance(
	ctx context.Context, req *dicerdv1.StopInstanceRequest,
) (*dicerdv1.Instance, error) {
	if err := h.instanceManager.Stop(ctx, req.GetName()); err != nil {
		return nil, err
	}
	return h.viewNamed(req.GetName())
}

// PauseInstance pauses a running instance.
func (h *instanceHandler) PauseInstance(
	ctx context.Context, req *dicerdv1.PauseInstanceRequest,
) (*dicerdv1.Instance, error) {
	if err := h.instanceManager.Pause(ctx, req.GetName()); err != nil {
		return nil, err
	}
	return h.viewNamed(req.GetName())
}

// StandbyInstance freezes a running or paused instance to disk. See
// instance.Manager.Standby.
func (h *instanceHandler) StandbyInstance(
	ctx context.Context, req *dicerdv1.StandbyInstanceRequest,
) (*dicerdv1.Instance, error) {
	if err := h.instanceManager.Standby(ctx, req.GetName()); err != nil {
		return nil, err
	}
	return h.viewNamed(req.GetName())
}

// ResizeInstance changes a running instance's vCPUs and memory. See
// instance.Manager.Resize. What the request leaves out stays as it is.
func (h *instanceHandler) ResizeInstance(
	ctx context.Context, req *dicerdv1.ResizeInstanceRequest,
) (*dicerdv1.Instance, error) {
	if req.Vcpus == nil && req.MemoryBytes == nil {
		return nil, errdefs.InvalidArgument("a resize needs vcpus, memory_bytes or both")
	}
	instance, err := h.instanceManager.Instance(req.GetName())
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

	if err := h.instanceManager.Resize(ctx, instance.ID, want); err != nil {
		return nil, err
	}
	return h.viewNamed(instance.ID)
}

// ResumeInstance resumes a paused instance.
func (h *instanceHandler) ResumeInstance(
	ctx context.Context, req *dicerdv1.ResumeInstanceRequest,
) (*dicerdv1.Instance, error) {
	if err := h.instanceManager.Resume(ctx, req.GetName()); err != nil {
		return nil, err
	}
	return h.viewNamed(req.GetName())
}

// RenameInstance changes a stopped instance's name.
func (h *instanceHandler) RenameInstance(
	ctx context.Context, req *dicerdv1.RenameInstanceRequest,
) (*dicerdv1.Instance, error) {
	renamed, err := h.instanceManager.Rename(ctx, req.GetName(), req.GetNewName())
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
	if err := h.instanceManager.Delete(ctx, req.GetName(), req.GetForce()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// GetInstance returns an instance with its status.
func (h *instanceHandler) GetInstance(
	_ context.Context, req *dicerdv1.GetInstanceRequest,
) (*dicerdv1.Instance, error) {
	return h.viewNamed(req.GetName())
}

// ListInstances lists every instance with its status, sorted by name.
func (h *instanceHandler) ListInstances(
	_ context.Context, _ *dicerdv1.ListInstancesRequest,
) (*dicerdv1.ListInstancesResponse, error) {
	instances := h.instanceManager.Instances()

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
	return viewInstance(h.instanceManager, instance)
}

// viewNamed assembles the API representation of the instance named or with
// the ID nameOrID, as it is now.
func (h *instanceHandler) viewNamed(nameOrID string) (*dicerdv1.Instance, error) {
	instance, err := h.instanceManager.Instance(nameOrID)
	if err != nil {
		return nil, err
	}
	return h.view(instance)
}

// viewInstance assembles an instance's spec, status, address and health.
func viewInstance(instanceManager *instance.Manager, spec instance.Spec) (*dicerdv1.Instance, error) {
	status, err := instanceManager.Status(spec.ID)
	if err != nil {
		return nil, err
	}

	if allocation, err := instanceManager.Allocation(spec.ID); err == nil {
		status.IP, status.MAC = allocation.IP, allocation.MAC
	}
	if check, health, ok := instanceManager.Health(spec.ID); ok {
		status.HealthCheck, status.Health = &check, &health
	}

	return instanceToProto(instance.Instance{Spec: spec, Status: status}), nil
}
