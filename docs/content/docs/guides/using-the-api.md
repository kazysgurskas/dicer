---
title: Using the API
weight: 14
description: "Call the gRPC API from Go, Python or the shell."
icon: code
related:
  - /docs/reference/api
  - /docs/guides/remote-access
  - /docs/reference/events
---

The `dicer` command line does everything through the daemon's gRPC API, and
any program can do the same. The API is one service,
`dicerd.v1.DaemonService`, defined in
[`proto/dicerd/v1/dicerd.proto`](https://github.com/konradasb/dicer/blob/main/proto/dicerd/v1/dicerd.proto).
The [API reference](../../reference/api) lists every call and message.

The API is covered by Dicer's versioning. A patch release never breaks it.
Until 1.0.0, a minor release may, and its release notes say what changed and
what to do about it.

## Connecting

The daemon serves the API on its Unix socket, `unix:///run/dicer/dicer.sock`,
which root and the `dicer` group can open. It can also serve it on a TCP
listener, with TLS. See [Remote access](../remote-access) for the listener
and its certificates.

## Errors

A failed call ends with a gRPC status. Its code says what kind of failure it
was: `NOT_FOUND` for a resource that does not exist, for example, or
`FAILED_PRECONDITION` for one in the wrong state for the call. Its message
says what went wrong, for a person to read. The
[API reference](../../reference/api) lists the codes. Match on the code, not
the message, which may change.

## From Go

The `github.com/konradasb/dicer` package is the Go client. Its calls are
grouped by the resource they act on, take and return plain Go types, and
hide the gRPC streams behind readers, writers and an `exec.Cmd`-like
command:

```console
$ go get github.com/konradasb/dicer
```

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/konradasb/dicer"
)

func main() {
	ctx := context.Background()

	// The local daemon, on its socket.
	c, err := dicer.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	instance, err := c.Instances.Create(ctx, dicer.InstanceSpec{
		Name:        "web",
		ImageRef:    "nginx:1.27",
		VCPUs:       1,
		MemoryBytes: 512 << 20,
		DiskBytes:   10 << 30,
		Ports:       []dicer.PortMapping{{HostPort: 8080, GuestPort: 80}},
	}, dicer.CreateOptions{Start: true})
	switch {
	case err == nil:
		fmt.Printf("%s is %s at %s\n", instance.Name, instance.State, instance.IP)
	case errors.Is(err, dicer.ErrAlreadyExists):
		fmt.Println("web exists already")
	default:
		log.Fatal(err)
	}

	// Run a command in it.
	out, err := c.Instances.Command("web", "nginx", "-v").Output(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s", out)

	// Follow its console until it stops.
	console, err := c.Instances.Logs(ctx, "web", dicer.LogOptions{Follow: true})
	if err != nil {
		log.Fatal(err)
	}
	defer console.Close()
	if _, err := io.Copy(os.Stdout, console); err != nil {
		log.Fatal(err)
	}
}
```

A failed call returns an error that matches the kind of failure with
`errors.Is`: `dicer.ErrNotFound` for `NOT_FOUND`, `dicer.ErrFailedPrecondition`
for `FAILED_PRECONDITION`, and so on. Its message is the daemon's. A command
that exits with a status other than 0 returns a `*dicer.ExitError`, which
holds the status.

| To | Call |
|----|------|
| Define, start, stop and delete instances | `c.Instances.Create`, `Start`, `Stop`, `Delete` |
| Run a command | `c.Instances.Command(name, args...)`, then `Run`, `Output` or `Start` and `Wait` |
| Copy files | `c.Instances.WriteFile`, `ReadFile`, `CopyTo`, `CopyFrom` |
| Wait for an instance to stop | `c.Instances.Wait` |
| Take, restore and fork snapshots | `c.Snapshots.Create`, `Restore`, `Fork` |
| Pull images, with progress | `c.Images.Pull` |
| Follow what happens on the host | `c.Events` |

The [package documentation](https://pkg.go.dev/github.com/konradasb/dicer)
lists every call. A `dicer.Client` is safe for concurrent use; make one and
share it.

For a daemon's TCP listener, give its address and a TLS configuration:

```go
cert, err := tls.LoadX509KeyPair("client.pem", "client-key.pem")
// …
roots := x509.NewCertPool()
roots.AppendCertsFromPEM(caPEM)

c, err := dicer.NewClient(
	dicer.WithAddress("dicer1.example.com:7443"),
	dicer.WithTLS(&tls.Config{
		RootCAs:      roots,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}),
)
```

`dicer.WithKeepalive` sets how often the client checks that the daemon is
still there (see [Remote access](../remote-access#connections-that-go-quiet)).
`dicer.WithDialOptions` adds any other gRPC dial option, such as an
interceptor.

## From other languages

Generate a client from the proto file with your language's gRPC tools. The
file imports only Google's well-known types, which the tools include. For
Python, with the repository cloned into `dicer`:

```console
$ git clone https://github.com/konradasb/dicer
$ pip install grpcio grpcio-tools
$ python -m grpc_tools.protoc -I dicer/proto \
    --python_out=. --grpc_python_out=. dicerd/v1/dicerd.proto
```

```python
import grpc
from dicerd.v1 import dicerd_pb2, dicerd_pb2_grpc

with grpc.insecure_channel("unix:///run/dicer/dicer.sock") as channel:
    daemon = dicerd_pb2_grpc.DaemonServiceStub(channel)

    for inst in daemon.ListInstances(dicerd_pb2.ListInstancesRequest()).instances:
        print(inst.name, dicerd_pb2.InstanceState.Name(inst.state), inst.ip)

    try:
        daemon.GetInstance(dicerd_pb2.GetInstanceRequest(name="db"))
    except grpc.RpcError as e:
        if e.code() == grpc.StatusCode.NOT_FOUND:
            print("no db")
```

`insecure_channel` is right for the socket, which has no TLS. For a TCP
listener with TLS, use `grpc.secure_channel` with
`grpc.ssl_channel_credentials`.

## From the shell

The daemon does not serve gRPC reflection, so give
[grpcurl](https://github.com/fullstorydev/grpcurl) the proto file, from the
cloned repository:

```console
$ grpcurl -plaintext -unix \
    -import-path dicer/proto -proto dicerd/v1/dicerd.proto \
    /run/dicer/dicer.sock dicerd.v1.DaemonService/GetHostInfo
$ grpcurl -plaintext -unix \
    -import-path dicer/proto -proto dicerd/v1/dicerd.proto \
    -d '{"name": "web"}' \
    /run/dicer/dicer.sock dicerd.v1.DaemonService/GetInstance
```

For a TCP listener with TLS, replace `-plaintext -unix` and the socket with
`-cacert ca.pem -cert client.pem -key client-key.pem` and the daemon's
`HOST:PORT`.
