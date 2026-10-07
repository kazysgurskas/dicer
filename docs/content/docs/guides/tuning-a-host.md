---
title: Tuning a host
weight: 17
description: "Set up a production host's firmware, operating system and data drives for Dicer, and size Dicer's configuration to it."
icon: adjustments
related:
  - /docs/guides/choosing-hardware
  - /docs/guides/capacity
  - /docs/concepts/hypervisors
---

This page sets up a production host for Dicer, such as the
[example server](../choosing-hardware). Most of the security settings
follow
[Firecracker's production host recommendations](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md),
and apply to Cloud Hypervisor too.

## Firmware

| Setting | Value | Why |
|---|---|---|
| SVM | On | AMD's hardware virtualisation, which KVM needs. |
| NUMA nodes per socket | NPS1 | Keeps the socket one NUMA node. |
| System profile | Performance | Cores don't drop into deep sleep states between guests' bursts of work. |
| SMT | Off on multi-tenant hosts | Two threads that share a core can leak data to each other through side channels. Leave it on if all instances belong to one tenant. |

Update the firmware before the host goes into service, and keep the CPU's
microcode current through the operating system's updates.

## Operating system

| Setting | Why |
|---|---|
| No swap | Guests' memory should never be written to a swap device. With no memory overcommit, the host doesn't need it. |
| Kernel Samepage Merging off | Merging identical pages between guests lets one guest learn what another has in memory. It is off by default. |
| `quiet loglevel=1` | A slow console slows down every snapshot restore, because the kernel logs a message for each network device it creates. |
| `kvm.nx_huge_pages=never` | Turns off the mitigation for iTLB multihit, which AMD CPUs don't need. On Linux 6.1 and later, it slows down the creation of every guest. |

Keep the kernel's other mitigations on, and install the distribution's
security updates.

Turn swap off, and remove its entry from `/etc/fstab`:

```console
$ sudo swapoff -a
```

Check that the CPU is not affected by iTLB multihit:

```console
$ cat /sys/devices/system/cpu/vulnerabilities/itlb_multihit
Not affected
```

Then add the kernel arguments, and reboot:

{{< tabs >}}
  {{< tab name="Debian, Ubuntu" >}}
  Add `quiet loglevel=1 kvm.nx_huge_pages=never` to `GRUB_CMDLINE_LINUX`
  in `/etc/default/grub`, then:

  ```console
  $ sudo update-grub
  $ sudo reboot
  ```
  {{< /tab >}}
  {{< tab name="RHEL" >}}
  ```console
  $ sudo grubby --update-kernel=ALL --args="quiet loglevel=1 kvm.nx_huge_pages=never"
  $ sudo reboot
  ```
  {{< /tab >}}
{{< /tabs >}}

## Data drives

Put `/var/lib/dicer` on XFS. Dicer copies an instance's overlay disk every
time it snapshots, forks or restores it. XFS makes that copy a reflink,
which is instant and takes no space until one side is written to. On ext4,
every byte the instance has written is copied. Btrfs can reflink too, but
fragments disk images that guests keep writing to in place. Keep all of
`/var/lib/dicer` on one filesystem, because a reflink cannot cross from
one filesystem to another.

Do this before installing Dicer. Find the four data drives first, because
the boot drives may be NVMe devices too:

```console
$ lsblk -d -o NAME,SIZE,MODEL
$ sudo mdadm --create /dev/md0 --level=10 --raid-devices=4 \
    /dev/nvme1n1 /dev/nvme2n1 /dev/nvme3n1 /dev/nvme4n1
$ sudo mkfs.xfs -m reflink=1 /dev/md0
$ sudo mkdir -p /var/lib/dicer
$ echo "UUID=$(sudo blkid -s UUID -o value /dev/md0) /var/lib/dicer xfs defaults,noatime 0 2" \
    | sudo tee -a /etc/fstab
$ sudo mount /var/lib/dicer
```

Save the array, so that it is assembled at boot:

{{< tabs >}}
  {{< tab name="Debian, Ubuntu" >}}
  ```console
  $ sudo mdadm --detail --scan | sudo tee -a /etc/mdadm/mdadm.conf
  $ sudo update-initramfs -u
  ```
  {{< /tab >}}
  {{< tab name="RHEL" >}}
  ```console
  $ sudo mdadm --detail --scan | sudo tee -a /etc/mdadm.conf
  $ sudo dracut -f
  ```
  {{< /tab >}}
{{< /tabs >}}

Check that reflinks work. The copy should take a few milliseconds. On a
filesystem that cannot reflink, `cp` fails at once:

```console
$ sudo dd if=/dev/zero of=/var/lib/dicer/a bs=1M count=1024 status=none
$ time sudo cp --reflink=always /var/lib/dicer/a /var/lib/dicer/b
$ sudo rm /var/lib/dicer/a /var/lib/dicer/b
```

## Dicer's configuration

[Install Dicer](../../getting-started/installation), and size its
[capacity](../capacity) to the host. For the example server:

```yaml {filename="/etc/dicerd/config.yaml"}
resources:
  cpu_overcommit: 4
  memory_overcommit: 1
  reserved_memory_bytes: 34359738368   # 32 GiB
images:
  gc_max_size: 200GiB
```

The daemon can then give out 512 vCPUs, or 256 with SMT off, and 736 GiB
of memory. For
CPU-heavy workloads, such as builds or model inference, lower
`cpu_overcommit` to 2 or 1. Restart the daemon after changing the file.

Both hypervisors run on the same host, and each instance picks one with
`--hypervisor-type`:

- **Firecracker** suits many forks of one snapshot. A fork reads its memory
  from the snapshot only as the guest uses it, and the forks share those
  pages in the host's page cache. Eight forks of a 2 GiB instance added
  under 300 MiB to the host's memory in use.
- **Cloud Hypervisor** suits long-running instances. It can add vCPUs to a
  running guest. After a restore, it reads the rest of the guest's memory
  in the background, so each fork soon holds all of its memory.

See [Differences](../../concepts/hypervisors#differences).

## Checking the host

```console
$ ls /dev/kvm                                      # KVM is there
$ cat /sys/devices/system/cpu/smt/control          # on, or off as decided
$ swapon --show                                    # prints nothing
$ cat /sys/kernel/mm/ksm/run                       # 0
$ ls /proc/sys/vm/unprivileged_userfaultfd         # exists
$ findmnt -n -o FSTYPE /var/lib/dicer              # xfs
$ dicer info
```

Without userfaultfd, Cloud Hypervisor restores all of a guest's memory
before the guest resumes, and a restore takes seconds instead of
milliseconds. `dicer info` shows the vCPUs and memory the daemon gives out.
