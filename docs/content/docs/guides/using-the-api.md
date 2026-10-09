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

The [Go client reference](../../reference/go-client) lists every call. A
`dicer.Client` is safe for concurrent use; make one and share it.

For a daemon's TCP listener, give its address and a token, which
`dicer token create` makes on the daemon's host (see
[Remote access](../remote-access)):

```go
c, err := dicer.NewClient(
	dicer.WithAddress("dicer1.example.com:7443"),
	dicer.WithToken(os.Getenv("DICER_PROD_TOKEN")),
)
```

The token also carries the fingerprint of the daemon's certificate, which
the client checks the daemon by. A daemon served with a certificate from an
authority of your own needs `dicer.WithTLS` too, with the authority in its
`RootCAs`. A call the daemon refuses the token of fails with
`dicer.ErrUnauthenticated`. `c.Tokens` makes, rotates and deletes tokens.

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

`insecure_channel` is right for the socket, which has no TLS. A daemon's
TCP listener takes TLS and a token, sent with every call as the
`authorization` header, `Bearer TOKEN`. The token ends with the
fingerprint of the daemon's certificate: the SHA-256 of its public key,
after the last underscore. Check the certificate the daemon presents
against it before sending anything, or give the daemon a certificate of
your own with `server.crt_file` and check it as usual.

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

For a TCP listener, replace `-plaintext -unix` and the socket with
`-H "authorization: Bearer $TOKEN"` and the daemon's `HOST:PORT`. grpcurl
cannot check the daemon by a token's fingerprint, so this needs a daemon
served with a certificate of `server.crt_file` that grpcurl can verify.
Its `-insecure` would send the token to whatever answers at the address.
