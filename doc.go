// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package dicer is the Go client of a Dicer daemon.
//
// A Client connects to the local daemon's socket by default, or to a
// daemon's TCP listener, with TLS. Its calls are grouped by the resource
// they act on:
//
//	c, err := dicer.NewClient()
//	if err != nil {
//		return err
//	}
//	defer c.Close()
//
//	instance, err := c.Instances.Create(ctx, dicer.InstanceSpec{
//		Name:        "web",
//		ImageRef:    "nginx:1.27",
//		VCPUs:       1,
//		MemoryBytes: 512 << 20,
//		DiskBytes:   10 << 30,
//	}, dicer.CreateOptions{Start: true})
//
// A command runs in an instance as an exec.Cmd runs on this machine:
//
//	out, err := c.Instances.Command("web", "cat", "/etc/os-release").Output(ctx)
//
// A daemon on another machine is named by the address of its TCP listener,
// and reached with a token that `dicer token create` makes on its host:
//
//	c, err := dicer.NewClient(dicer.WithAddress("host:7443"), dicer.WithToken(token))
//
// The socket is protected by its file permissions. Over TCP, the connection
// uses TLS, and the daemon is checked by the fingerprint the token carries.
//
// A failed call returns an error that matches the kind of failure it was,
// such as ErrNotFound, and whose message is the daemon's:
//
//	if errors.Is(err, dicer.ErrNotFound) {
//		...
//	}
//
// The API itself is the gRPC service in proto/dicerd/v1, from which clients
// in other languages are generated.
package dicer
