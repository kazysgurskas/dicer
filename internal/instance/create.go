// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"
	"net"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/humanize"
	"github.com/konradasb/dicer/internal/image"
	"github.com/konradasb/dicer/internal/image/reference"
	"github.com/konradasb/dicer/internal/network"
)

// Create records a new instance's definition, first pulling its image as
// pull says, and pins it to the digest its image reference resolves to. It
// refuses an invalid definition, a name already taken, and one that could
// never start, before pulling anything. It boots nothing: see Start. Nothing
// is recorded if the image cannot be had.
func (m *Manager) Create(ctx context.Context, instance Spec, pull image.PullPolicy) error {
	if err := instance.Validate(); err != nil {
		return err
	}
	if _, err := m.store.Instance(instance.Name); err == nil {
		return errdefs.Exists("instance %q already exists", instance.Name)
	}
	if err := m.checkCanStart(instance); err != nil {
		return err
	}

	// Before the definition, so that one whose image cannot be had is never
	// seen, even for as long as a pull takes. The error names the image.
	resolved, err := m.images.Ensure(ctx, instance.ImageRef, pull)
	if err != nil {
		return err
	}
	instance.ImageDigest = resolved.Digest

	if err := m.store.CreateInstance(instance); err != nil {
		return err
	}
	m.record(instance, event.ActionCreated, fmt.Sprintf("Created instance from image %s with %s, %s memory, %s disk; restart policy %s", reference.FamiliarString(instance.ImageRef), humanize.Count(instance.VCPUs, "vCPU"), humanize.Bytes(instance.MemoryBytes), humanize.Bytes(instance.DiskBytes), instance.Restart), map[string]string{"image": instance.ImageRef})
	return nil
}

// checkCanStart refuses a definition that could never start on this host: a
// missing kernel, network or volume, a static IP its network cannot assign,
// a host directory the daemon does not allow or cannot find, or more
// resources than the host allows.
func (m *Manager) checkCanStart(instance Spec) error {
	if _, err := m.store.Kernel(instance.KernelName); err != nil {
		return errdefs.InvalidArgument("%v", err)
	}
	n, err := m.store.Network(instance.NetworkName)
	if err != nil {
		return errdefs.InvalidArgument("%v", err)
	}
	if instance.StaticIP != "" {
		subnet, err := network.ParseSubnet(n.Subnet)
		if err != nil {
			return err
		}
		ip := net.ParseIP(instance.StaticIP)
		if ip == nil || !network.Assignable(subnet, ip) || ip.Equal(net.ParseIP(n.Gateway)) {
			return errdefs.InvalidArgument(
				"static IP %q is not an address network %q can assign: its subnet is %s, and its gateway %s",
				instance.StaticIP, n.Name, n.Subnet, n.Gateway)
		}
	}
	for _, mount := range instance.Mounts {
		switch mount.Type {
		case MountTypeVolume:
			if _, err := m.store.Volume(mount.Source); err != nil {
				return errdefs.InvalidArgument("%v", err)
			}
		case MountTypeDirectory:
			// Checked again at each start, which is what shares it.
			if _, err := m.allowedDirectories.Resolve(mount.Source); err != nil {
				return errdefs.InvalidArgument("mount on %s: %v", mount.Target, err)
			}
		}
	}
	return m.CheckResources(instance.MaxResources())
}
