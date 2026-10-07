---
title: Running Docker inside an instance
weight: 1
description: "Run Docker inside an instance, to build images and run containers in a machine of their own."
icon: cube
related:
  - /docs/guides/files-and-volumes
  - /docs/guides/working-inside-guests
  - /docs/concepts/kernels
---

An instance can run Docker like any Linux machine. Its Docker is its own:
behind the hypervisor, it cannot see the host's Docker or another
instance's, and its containers cannot reach them.

The image here is `docker:27-dind`, Docker's official Docker-in-Docker
image, which runs the Docker daemon as its main process.

## Run Docker

```console
$ dicer volume create docker-data --size 20GiB
$ dicer run -d --name docker --vcpus 2 --memory 2GiB \
    --mount source=docker-data,target=/var/lib/docker \
    docker:27-dind
```

Docker keeps its images and containers on the volume, so size it for them.
Docker and its containers share the instance's memory, so give it 2 GiB or
more.

## Use it

```console
$ dicer exec docker docker info --format '{{.Driver}}'
overlay2
$ dicer exec docker docker run --rm hello-world
```

`docker info` may take a few seconds to answer while `dockerd` starts.

`docker run -p` publishes a container's port on the instance, not on the
host. To reach it from outside the host, publish the same port on the
instance too. With `-p 8080:8080` added to the `dicer run` above, this
serves nginx on the host's port 8080:

```console
$ dicer exec docker docker run -d -p 8080:80 nginx:1.27
```

## Good to know

- Docker needs the volume. Without it, Docker's data sits on the instance's
  root filesystem, which is itself an overlay, and Docker falls back to its
  slow `vfs` storage driver. On the volume, which is ext4, it uses
  `overlay2`. Only one running instance at a time can write to a volume, so
  each instance that runs Docker needs its own. See
  [Volumes](../../guides/files-and-volumes#volumes).
- The volume keeps Docker's images and containers after the instance is
  deleted, until the volume itself is deleted.
- Dicer's [default kernel](../../concepts/kernels#the-default-kernel) has
  iptables' legacy tables, not nftables. `docker:dind` uses them by itself.
  An image that installs Docker from a distribution's packages may not, and
  `dockerd` then fails with `Failed to initialize nft: Protocol not
  supported`. On Debian or Ubuntu, build such an image with
  `RUN update-alternatives --set iptables /usr/sbin/iptables-legacy`.
- The kernel has no IPv6. Docker warns that it cannot set up `ip6tables`,
  and carries on over IPv4.
- Each instance's Docker pulls images for itself, with no cache shared
  between instances or with the host. Where many instances pull the same
  images, a pull-through registry cache near the host saves time and
  bandwidth.
