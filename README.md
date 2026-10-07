<h1>
  <img src="docs/static/images/logo.svg" alt="" width="40" height="40" align="top">
  Dicer
</h1>

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/konradasb/dicer/badge)](https://scorecard.dev/viewer/?uri=github.com/konradasb/dicer)
[![test](https://github.com/konradasb/dicer/actions/workflows/test.yaml/badge.svg)](https://github.com/konradasb/dicer/actions/workflows/test.yaml)
[![lint](https://github.com/konradasb/dicer/actions/workflows/lint.yaml/badge.svg)](https://github.com/konradasb/dicer/actions/workflows/lint.yaml)
[![security](https://github.com/konradasb/dicer/actions/workflows/security.yaml/badge.svg)](https://github.com/konradasb/dicer/actions/workflows/security.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/konradasb/dicer.svg)](https://pkg.go.dev/github.com/konradasb/dicer)
[![License: MIT](https://img.shields.io/github/license/konradasb/dicer)](LICENSE)

Run virtual machines from container images, on one host.

Dicer pulls an OCI image, converts it to a read-only root filesystem and boots
it as a VM under Cloud Hypervisor or Firecracker, with a writable overlay on
top.

## Requirements

- Linux with KVM (`/dev/kvm`)
- `erofs-utils` (`mkfs.erofs`) and `e2fsprogs` (`mke2fs`)
- IPv4 forwarding enabled

## Install

```console
curl -fsSL https://raw.githubusercontent.com/konradasb/dicer/main/scripts/install.sh | bash
```

This builds `dicer` and `dicerd` from source and installs `dicerd` as a
systemd service.

## Quickstart

The daemon creates a network named `default`, and a kernel named `default`
that is [Dicer's kernel](https://github.com/konradasb/dicer-kernel). An
instance that names no network or kernel uses them, so there is nothing to
set up first:

```console
$ dicer run -d --name web -p 8080:80 nginx:1.27
$ dicer ps
$ dicer exec web sh
$ dicer logs -f web
$ dicer stop web
$ dicer rm web
```

See `dicer --help` for every command.

## Configuration

`dicerd` reads `/etc/dicerd/config.yaml`. The file is optional: every key has
a default, and unknown keys are rejected. The
[configuration reference](docs/content/docs/reference/configuration.md) shows
every key with its default, and `dicerd validate` checks a file before the
daemon uses it.

## License

MIT. See [LICENSE](LICENSE).
