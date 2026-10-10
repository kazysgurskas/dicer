// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cloudhypervisor

import (
	"path"

	"github.com/konradasb/dicer/internal/hypervisor"
)

// mediatedDeviceDir is the sysfs directory holding the host's mediated
// devices, by UUID. VFIO opens a mediated device by its path there.
const mediatedDeviceDir = "/sys/bus/mdev/devices"

// vmConfig translates a VM specification into Cloud Hypervisor's API
// representation. The serial port is the console; the virtio console is
// off.
func vmConfig(spec hypervisor.VMSpec) VmConfig {
	return VmConfig{
		Payload: PayloadConfig{
			Kernel:    new(spec.Boot.KernelPath),
			Cmdline:   new(spec.Boot.KernelArgs),
			Initramfs: new(spec.Boot.InitrdPath),
		},
		Cpus:    new(cpusConfig(spec.CPU)),
		Memory:  new(memoryConfig(spec.Memory, len(spec.Filesystems) > 0)),
		Disks:   new(mapSlice(spec.Disks, diskConfig)),
		Fs:      optionalSlice(mapSlice(spec.Filesystems, fsConfig)),
		Serial:  &SerialConfig{Mode: ConsoleModeFile, File: new(spec.Console.Path)},
		Console: &ConsoleConfig{Mode: ConsoleModeOff},
		Net:     optionalSlice(mapSlice(spec.NetworkInterfaces, netConfig)),
		Vsock:   vsockConfig(spec.Vsock),
		Devices: optionalSlice(deviceConfigs(spec)),
	}
}

func cpusConfig(c hypervisor.CPUConfig) CpusConfig {
	cpus := CpusConfig{BootVcpus: c.Count, MaxVcpus: c.Count}
	if c.MaxCount > 0 {
		cpus.MaxVcpus = c.MaxCount
	}
	if len(c.Affinity) > 0 {
		cpus.Affinity = new(mapSlice(c.Affinity, func(a hypervisor.CPUAffinity) CpuAffinity {
			return CpuAffinity{Vcpu: a.VCPU, HostCpus: a.HostCPUs}
		}))
	}
	if t := c.Topology; t != nil {
		cpus.Topology = &CpuTopology{
			ThreadsPerCore: new(t.ThreadsPerCore),
			CoresPerDie:    new(t.CoresPerDie),
			DiesPerPackage: new(t.DiesPerPackage),
			Packages:       new(t.Packages),
		}
	}
	return cpus
}

// virtioMemAlignment is what Cloud Hypervisor requires the memory set aside
// for virtio-mem to be a multiple of.
const virtioMemAlignment = 128 << 20

// memoryConfig translates the memory, rounding what is set aside for hotplug
// up to virtioMemAlignment. A vhost-user device, as a shared directory is,
// reaches into guest memory from another process, so the memory must be
// shared with it.
func memoryConfig(m hypervisor.MemoryConfig, shared bool) MemoryConfig {
	memory := MemoryConfig{Size: m.SizeBytes}
	if shared {
		memory.Shared = new(true)
	}
	if m.HotplugBytes > 0 {
		memory.HotplugSize = new((m.HotplugBytes + virtioMemAlignment - 1) / virtioMemAlignment * virtioMemAlignment)
		memory.HotplugMethod = new("VirtioMem")
	}
	return memory
}

// diskConfig translates a disk. A rate limit is a token bucket holding what
// the disk may do in a second, refilled every second.
//
// Every disk is declared raw. From v51, Cloud Hypervisor refuses writes to
// sector 0 of a raw disk whose type it had to guess, and ext4 writes its
// superblock there. Older versions ignore the field.
func diskConfig(d hypervisor.DiskConfig) DiskConfig {
	disk := DiskConfig{Path: new(d.Path), ImageType: new(Raw)}
	if d.ReadOnly {
		disk.Readonly = new(true)
	}
	if d.RateLimitBytesPerSecond > 0 || d.RateLimitIOPS > 0 {
		disk.RateLimiterConfig = &RateLimiterConfig{
			Bandwidth: perSecondBucket(d.RateLimitBytesPerSecond),
			Ops:       perSecondBucket(d.RateLimitIOPS),
		}
	}
	return disk
}

// perSecondBucket returns a token bucket refilled with n tokens every
// second, or nil for none if n is zero.
func perSecondBucket(n int64) *TokenBucket {
	if n <= 0 {
		return nil
	}
	return &TokenBucket{Size: n, RefillTime: 1000}
}

// A shared directory's request queues, as virtiofsd serves them by default.
const (
	fsNumQueues = 1
	fsQueueSize = 1024
)

// fsConfig translates a shared directory.
func fsConfig(f hypervisor.FilesystemConfig) FsConfig {
	return FsConfig{Tag: f.Tag, Socket: f.Socket, NumQueues: fsNumQueues, QueueSize: fsQueueSize}
}

func netConfig(n hypervisor.NetworkInterfaceConfig) NetConfig {
	net := NetConfig{Tap: new(n.TAPDevice), Ip: new(n.IP), Mac: new(n.MAC), Mask: new(n.Netmask)}
	if n.MTU > 0 {
		net.Mtu = new(n.MTU)
	}
	return net
}

func vsockConfig(v *hypervisor.VsockConfig) *VsockConfig {
	if v == nil {
		return nil
	}
	return &VsockConfig{Cid: int64(v.CID), Socket: v.SocketPath}
}

// deviceConfigs lists the host devices passed through to the guest over
// VFIO: the PCI devices, then the GPU's mediated device.
func deviceConfigs(spec hypervisor.VMSpec) []DeviceConfig {
	devices := mapSlice(spec.PCIDevices, func(d hypervisor.PCIDeviceConfig) DeviceConfig {
		return DeviceConfig{Path: new(d.Path)}
	})
	if spec.GPU != nil {
		devices = append(devices, DeviceConfig{
			Path: new(path.Join(mediatedDeviceDir, spec.GPU.MediatedDeviceUUID)),
		})
	}
	return devices
}

// mapSlice returns f applied to each element of in.
func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// optionalSlice returns a pointer to s, or nil if s is empty, for fields the
// API omits when there is nothing in them.
func optionalSlice[T any](s []T) *[]T {
	if len(s) == 0 {
		return nil
	}
	return &s
}
