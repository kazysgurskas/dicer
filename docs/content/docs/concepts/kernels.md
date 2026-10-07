---
title: Kernels
weight: 4
description: "Why every instance boots a kernel of its own, the default kernel, and how to import another."
icon: chip
related:
  - /docs/concepts/hypervisors
  - /docs/concepts/images
  - /docs/guides/troubleshooting
---

A container image holds no kernel, because containers share their host's. A
virtual machine needs one of its own, so every instance boots a kernel, kept
apart from its image.

## The default kernel

The daemon defines a kernel named `default`: a release of
[Dicer's kernel](#kernel-requirements) for the host's architecture, pinned by
this version of Dicer. An instance that names no kernel boots it. Like any
kernel, it is downloaded the first time an instance boots with it, and
checked against its checksum.

A new version of Dicer may pin a newer release. When the daemon is upgraded,
it updates the default kernel and discards the old download. Instances that
use the default kernel boot the new one the next time they start. A running
instance keeps the kernel it booted, and so does one restored from a
snapshot or resumed from standby.

The default kernel cannot be deleted, and no other kernel can be imported
under its name.

## Importing a kernel

To boot a kernel of your own, import it by name, from a URL, for an
architecture:

```console
$ dicer kernel import custom-6.18 --arch x86_64 \
    --url https://example.com/kernels/vmlinux-6.18 \
    --sha256 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

Importing only records the kernel. It is downloaded the first time an
instance boots with it, and kept. With `--sha256`, the download is checked
against that checksum. The URL can also be a `file://` URL or an absolute
path on the host.

The kernel's architecture must be the host's: `x86_64` or `aarch64`.

## Kernel requirements

[dicer-kernel](https://github.com/konradasb/dicer-kernel) publishes the kernel
Dicer is tested with, for x86_64 and aarch64. It is a long-term Linux release
from kernel.org, built with Cloud Hypervisor's configuration plus what
Dicer's guests need. It boots under both [hypervisors](../hypervisors). Each
release lists its checksums in a `SHA256SUMS` file signed with cosign, and
carries the kernel's source and configuration.

You can use a kernel of your own if it has what Dicer's guests rely on:

- EROFS with LZ4 compression, to mount the image;
- ext4, for the overlay disk and volumes;
- overlayfs, to lay the overlay disk over the image;
- virtio block, network and vsock devices, and virtio-mmio for Firecracker;
- a serial console.

The configuration that dicer-kernel adds, in its `dicer.config`, is a good
place to start.

## Choosing a kernel

An instance names its kernel with `--kernel`. An instance that names none
boots the [default kernel](#the-default-kernel). Importing other kernels
does not change that.

## Kernel arguments

An instance boots with the kernel command line its hypervisor needs:

| Hypervisor | Default arguments |
|---|---|
| Cloud Hypervisor | `console=ttyS0 reboot=k panic=1` |
| Firecracker | `console=ttyS0 reboot=k panic=1 pci=off` |

`--kernel-args` replaces the default whole, so keep these arguments when you
add your own. Without `panic=1`, for example, a kernel panic hangs the guest
instead of ending the instance.
