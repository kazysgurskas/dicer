// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cloudhypervisor

import (
	"reflect"
	"testing"

	"github.com/konradasb/dicer/internal/hypervisor"
)

// TestVMConfigPassesThroughDevices checks that PCI devices and a GPU's
// mediated device all reach the VMM as VFIO devices, and that a guest with
// none sends no device list.
func TestVMConfigPassesThroughDevices(t *testing.T) {
	tests := []struct {
		name string
		spec hypervisor.VMSpec
		want *[]DeviceConfig
	}{
		{
			name: "none",
		},
		{
			name: "pci devices",
			spec: hypervisor.VMSpec{PCIDevices: []hypervisor.PCIDeviceConfig{
				{Path: "/sys/bus/pci/devices/0000:01:00.0"},
			}},
			want: &[]DeviceConfig{{Path: new("/sys/bus/pci/devices/0000:01:00.0")}},
		},
		{
			name: "gpu",
			spec: hypervisor.VMSpec{GPU: &hypervisor.GPUConfig{
				Profile:            "nvidia-35",
				MediatedDeviceUUID: "c2f8e1a4-0d6b-4c3e-9f5a-2b7d8e9f0a1b",
			}},
			want: &[]DeviceConfig{{Path: new("/sys/bus/mdev/devices/c2f8e1a4-0d6b-4c3e-9f5a-2b7d8e9f0a1b")}},
		},
		{
			name: "pci devices and gpu",
			spec: hypervisor.VMSpec{
				PCIDevices: []hypervisor.PCIDeviceConfig{{Path: "/sys/bus/pci/devices/0000:01:00.0"}},
				GPU:        &hypervisor.GPUConfig{MediatedDeviceUUID: "c2f8e1a4-0d6b-4c3e-9f5a-2b7d8e9f0a1b"},
			},
			want: &[]DeviceConfig{
				{Path: new("/sys/bus/pci/devices/0000:01:00.0")},
				{Path: new("/sys/bus/mdev/devices/c2f8e1a4-0d6b-4c3e-9f5a-2b7d8e9f0a1b")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmConfig(tt.spec).Devices
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Devices = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMemorySetAsideForHotplugIsAligned checks that the memory set aside for
// resizing is rounded up to the 128 MiB Cloud Hypervisor requires, and that
// none is set aside unless asked for.
func TestMemorySetAsideForHotplugIsAligned(t *testing.T) {
	const mib = 1 << 20
	tests := []struct {
		name         string
		hotplugBytes int64
		want         *int64
	}{
		{"none", 0, nil},
		{"aligned", 256 * mib, new(int64(256 * mib))},
		{"unaligned", 300 * mib, new(int64(384 * mib))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memoryConfig(hypervisor.MemoryConfig{SizeBytes: 512 * mib, HotplugBytes: tt.hotplugBytes}, false)
			if !reflect.DeepEqual(got.HotplugSize, tt.want) {
				t.Errorf("hotplug_size = %v, want %v", deref(got.HotplugSize), deref(tt.want))
			}
		})
	}
}

// deref returns what p points to, or nil, for a readable failure.
func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestDiskRateLimitIsPerSecondBuckets checks that a disk's rate limits
// become token buckets refilled every second, with no bucket for a limit
// left unset and no limiter for an unlimited disk.
func TestDiskRateLimitIsPerSecondBuckets(t *testing.T) {
	tests := []struct {
		name string
		disk hypervisor.DiskConfig
		want *RateLimiterConfig
	}{
		{"unlimited", hypervisor.DiskConfig{}, nil},
		{
			"bytes",
			hypervisor.DiskConfig{RateLimitBytesPerSecond: 1 << 20},
			&RateLimiterConfig{Bandwidth: &TokenBucket{Size: 1 << 20, RefillTime: 1000}},
		},
		{
			"bytes and operations",
			hypervisor.DiskConfig{RateLimitBytesPerSecond: 1 << 20, RateLimitIOPS: 500},
			&RateLimiterConfig{
				Bandwidth: &TokenBucket{Size: 1 << 20, RefillTime: 1000},
				Ops:       &TokenBucket{Size: 500, RefillTime: 1000},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskConfig(tt.disk).RateLimiterConfig; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rate_limiter_config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDisksAreDeclaredRaw checks that no disk is left for Cloud Hypervisor
// to guess the type of, which from v51 makes it refuse writes to sector 0.
func TestDisksAreDeclaredRaw(t *testing.T) {
	for _, disk := range []hypervisor.DiskConfig{{Path: "overlay.img"}, {Path: "rootfs.erofs", ReadOnly: true}} {
		if got := diskConfig(disk).ImageType; got == nil || *got != Raw {
			t.Errorf("image_type of %s = %v, want Raw", disk.Path, got)
		}
	}
}

func TestVMConfigSharesDirectories(t *testing.T) {
	spec := hypervisor.VMSpec{
		Memory: hypervisor.MemoryConfig{SizeBytes: 512 << 20},
		Filesystems: []hypervisor.FilesystemConfig{
			{Tag: "dicerfs0", Socket: "/run/dicer/instances/web/fs0.sock"},
			{Tag: "dicerfs1", Socket: "/run/dicer/instances/web/fs1.sock"},
		},
	}

	cfg := vmConfig(spec)
	if cfg.Fs == nil || len(*cfg.Fs) != 2 {
		t.Fatalf("fs = %v, want a device for each directory", cfg.Fs)
	}
	for i, fs := range *cfg.Fs {
		want := spec.Filesystems[i]
		if fs.Tag != want.Tag || fs.Socket != want.Socket || fs.NumQueues < 1 || fs.QueueSize < 1 {
			t.Errorf("fs[%d] = %+v, want tag %s on %s with queues", i, fs, want.Tag, want.Socket)
		}
	}
	// virtiofsd reaches into guest memory, which must be shared with it.
	if cfg.Memory.Shared == nil || !*cfg.Memory.Shared {
		t.Error("memory is not shared, which a vhost-user device needs")
	}
}

func TestVMConfigWithoutDirectories(t *testing.T) {
	cfg := vmConfig(hypervisor.VMSpec{Memory: hypervisor.MemoryConfig{SizeBytes: 512 << 20}})
	if cfg.Fs != nil {
		t.Errorf("fs = %v, want none", *cfg.Fs)
	}
	if cfg.Memory.Shared != nil {
		t.Error("memory is shared with no device that needs it")
	}
}
