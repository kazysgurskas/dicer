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

> [!WARNING]
> Dicer has not reached version 1.0, so nothing in it is stable yet. Until
> then, a minor release can break the API, the Go package, the command line,
> its JSON output, the configuration file or the daemon's saved state. Read
> the release notes before you upgrade. [RELEASES.md](RELEASES.md) explains
> how versions work.

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

Run nginx in a virtual machine of its own, and look around:

```console
$ dicer run -d --name web -p 8080:80 nginx:1.27
$ dicer ps
$ dicer exec web sh
$ dicer logs -f web
$ dicer stop web
$ dicer rm web
```

The instance joins the `default` network and boots the `default` kernel,
[Dicer's kernel](https://github.com/konradasb/dicer-kernel). The daemon
sets up both itself, and an instance uses them unless it names others.

See `dicer --help` for every command.

## Go client

The `github.com/konradasb/dicer` package does from a program what the
command line does:

```go
c, err := dicer.NewClient() // the local daemon
if err != nil {
	return err
}
defer c.Close()

_, err = c.Instances.Create(ctx, dicer.InstanceSpec{
	Name:        "web",
	ImageRef:    "nginx:1.27",
	VCPUs:       1,
	MemoryBytes: 512 << 20,
	DiskBytes:   10 << 30,
}, dicer.CreateOptions{Start: true})
if err != nil {
	return err
}

out, err := c.Instances.Command("web", "nginx", "-v").Output(ctx)
```

For more examples, and for calling the API from other languages, see
[Using the API](docs/content/docs/guides/using-the-api.md).

## Configuration

`dicerd` reads `/etc/dicerd/config.yaml`. The file is optional: every key has
a default, and unknown keys are rejected. The
[configuration reference](docs/content/docs/reference/configuration.md) shows
every key with its default, and `dicerd validate` checks a file before the
daemon uses it.

## Licence

MIT. See [LICENSE](LICENSE).
