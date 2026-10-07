---
title: Choosing hardware
weight: 16
description: "What to look for in a server for Dicer, with an example build for a datacenter rack."
icon: server
related:
  - /docs/guides/tuning-a-host
  - /docs/guides/capacity
  - /docs/getting-started/installation
---

Dicer runs on any Linux host with KVM. For production, the CPU's layout,
the memory channels and the data drives decide how many instances a host
can hold, and how fast snapshots, forks and standby are. This page explains
what to look for. Its example is a server for several hundred instances:

| Component | Model | Details |
|---|---|---|
| Chassis | Dell PowerEdge R6715 | 1U, one SP5 socket, two redundant power supplies |
| CPU | AMD EPYC 9555 | 64 Zen 5 cores, 128 threads, 3.2 GHz base, 256 MB L3 cache |
| Memory | 12 × 64 GiB DDR5 RDIMM | 768 GiB, ECC, one DIMM on each of the twelve channels |
| Boot drives | Dell BOSS-N1 | Two 480 GB M.2 NVMe drives, mirrored, for the operating system |
| Data drives | 4 × 7.68 TB E3.S NVMe | PCIe Gen5, no RAID controller: software RAID 10, about 15 TB, for `/var/lib/dicer` |
| Network | 2 × 25 GbE | Bonded with LACP to a pair of switches |

Any vendor's single-socket server with the same parts works as well.
Configured as in [Tuning a host](../tuning-a-host#dicers-configuration), it
can give out 512 vCPUs and 736 GiB of memory: enough for 368 instances of
2 GiB running at once.

## CPU

**Use one socket.** Dicer doesn't pin vCPUs to host CPUs, so the kernel
moves them freely. On a two-socket server, a vCPU and its guest's memory
can end up on different sockets, and every memory access then crosses
between them. A single socket with many cores avoids that: in its default
NPS1 mode, an EPYC socket is one NUMA node.

**Count threads, not cores.** Each hardware thread can run four vCPUs at
Dicer's default overcommit. On a host whose instances belong to different
tenants, turn SMT off, which halves the threads. See
[Tuning a host](../tuning-a-host#firmware).

**Check the instruction set for the workload.** Guests get the host's CPU
features. Model inference, compression and video encoding run much faster
with AVX2 or AVX-512. Every current server CPU has AVX2, but not every one
has AVX-512.

**Prefer bare metal.** A cloud virtual machine can run Dicer only with
nested virtualisation turned on, and its guests run slower than on bare
metal.

## Memory

**Fill every channel once.** EPYC has twelve memory channels. One DIMM on
each gives the full bandwidth. A second DIMM on a channel adds capacity,
but lowers the memory's speed.

**Use ECC.** Server memory always has it. ECC also guards against Rowhammer,
where one guest's memory accesses flip bits in another's.

**Size it for the instances running at once.** Each instance is committed
all the memory it is given, whether it uses it or not. Instances on standby
are committed none, so a host can hold many more instances than it runs at
once. Leave a reserve for the host and the hypervisors' own overhead:
32 GiB on the example server. See [Capacity](../capacity).

## Disks

**Keep the system and the data apart.** Mirrored boot drives hold the
operating system. The data drives hold `/var/lib/dicer` alone, so the
instances' disk traffic never competes with the system's, and the data
drives can be replaced without reinstalling.

**Use NVMe for data.** Taking a memory snapshot, or putting an instance on
standby, writes the guest's whole memory to disk. Waking it reads the
memory back. The drives' speed decides how long both take.

**Skip the RAID controller.** Linux's software RAID, `mdadm`, handles NVMe
drives without one. Use RAID 10: it writes faster than RAID 5 or 6, which
matters for snapshots and standby, and it survives the loss of any one
drive.

**Size it for memory too.** Besides images and what instances write, each
memory snapshot and each instance on standby takes as much disk as the
instance's memory. On the example server's 15 TB, 2,000 instances of
2 GiB on standby take 4 TB.

## Network

Two ports, bonded with LACP to a pair of switches, give a host's guests
their bandwidth and survive the loss of a link or a switch. 25 GbE suits a
few hundred instances. Dicer needs no configuration for a bond: it finds
the uplink from the default route.
