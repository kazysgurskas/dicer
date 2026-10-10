---
title: Sandboxing untrusted code
weight: 2
description: "Run code you don't trust, such as AI-generated scripts, each job in a throwaway machine that cannot reach the network or the host."
icon: shield-check
related:
  - /docs/concepts/networking
  - /docs/guides/snapshots
  - /docs/guides/using-the-api
---

Code you don't trust, such as a script an AI model wrote or a user
uploaded, is best run in a machine of its own. Each job here gets a fresh
fork of a machine that is already booted, with its packages installed, on a
network that cannot reach the outside world or the host. When the job is
done, the fork is deleted.

The jobs here are Python scripts that use numpy.

## Snapshot the base

```console
$ dicer network create sandbox --subnet 172.30.0.0/24 --internal
$ dicer run -d --name python-base --vcpus 1 --memory 512MiB \
    python:3.13-slim sleep infinity
$ dicer exec python-base pip install --quiet --root-user-action=ignore numpy
$ dicer snapshot create python-base python-base
Snapshot python-base of instance python-base created in 552ms (memory, 649.1 MiB)
$ dicer rm -f python-base
```

The base runs on the `default` network so that it can download packages.
Its forks will run on `sandbox`, an
[internal network](../../concepts/networking#internal-networks). The host
enforces that, so code in the guest cannot undo it, even as root. Every job
gets the base's vCPUs, memory and disk, so size the base for the largest
job.

## Run a job

This script does some work, and also tries to reach the internet and the
host:

```python {filename="job.py"}
import json, socket, urllib.request
import numpy as np

result = {"mean": float(np.arange(1, 101).mean())}

try:
    urllib.request.urlopen("https://example.com", timeout=3)
    result["internet"] = "reachable"
except OSError as e:
    result["internet"] = f"blocked ({type(e).__name__})"

try:
    socket.create_connection(("host.dicer.internal", 22), timeout=3)
    result["host"] = "reachable"
except OSError as e:
    result["host"] = f"blocked ({type(e).__name__})"

json.dump(result, open("/tmp/result.json", "w"))
```

Run it in a fresh sandbox, copy out its result, and delete the sandbox:

```console
$ dicer snapshot fork python-base job-1 --network sandbox
Instance job-1 forked from snapshot python-base in 445ms (172.30.0.243)
$ dicer exec -T --timeout 30s job-1 python3 - < job.py
$ dicer cp job-1:/tmp/result.json .
$ dicer rm -f job-1
```

`python3 -` reads the script from standard input, so it never has to be
copied into the guest. `dicer exec` exits with the script's exit code, or
124 if `--timeout` killed it. Both connections were blocked:

```console
$ cat result.json
{"mean": 50.5, "internet": "blocked (URLError)", "host": "blocked (gaierror)"}
```

## From a program

A service that runs jobs does the same through the
[Go client](../../guides/using-the-api#from-go). This program runs the
script on its standard input in a new sandbox, streams its output, and
exits with its exit code:

```go {filename="main.go"}
// Command sandbox runs the Python script on its standard input in a new
// fork of the python-base snapshot, and deletes the fork afterwards. It
// exits with the script's exit code.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/konradasb/dicer"
)

func main() {
	script, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	client, err := dicer.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	code, err := runJob(context.Background(), client, script)
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(code)
}

// runJob forks a sandbox, runs script in it with python3, and deletes the
// sandbox, whatever happens.
func runJob(ctx context.Context, client *dicer.Client, script []byte) (int, error) {
	sandbox, err := client.Snapshots.Fork(ctx, "python-base", dicer.ForkOptions{NetworkName: "sandbox"})
	if err != nil {
		return 0, fmt.Errorf("fork a sandbox: %w", err)
	}
	defer func() {
		_ = client.Instances.Delete(context.WithoutCancel(ctx), sandbox.Name, dicer.DeleteOptions{Force: true})
	}()

	cmd := client.Instances.Command(sandbox.Name, "python3", "-")
	cmd.Stdin = bytes.NewReader(script)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Timeout = 30 * time.Second

	var exitErr *dicer.ExitError
	switch err := cmd.Run(ctx); {
	case errors.As(err, &exitErr):
		return exitErr.Code, nil
	case err != nil:
		return 0, err
	default:
		return 0, nil
	}
}
```

```console
$ echo 'print(1/0)' | ./sandbox
Traceback (most recent call last):
  File "<stdin>", line 1, in <module>
ZeroDivisionError: division by zero
$ echo $?
1
```

A job takes about two seconds from fork to deletion. To collect a file the
script writes, read it with `client.Instances.ReadFile` before the sandbox
is deleted.

## Good to know

- A sandbox cannot reach the outside world, other networks, any service on
  the host (including Dicer's API) or upstream nameservers. Sandboxes on
  the same network can reach each other, unless the network is also
  created with `--isolated`.
- Code in a sandbox runs as root in its own kernel, and cannot see the
  host's processes or files, or other sandboxes'.
- A sandbox can use only the base's vCPUs, memory and overlay disk, 10 GiB
  unless `--disk` says otherwise. Limit its disk with `--disk-rate` and
  `--disk-iops` on the base. See
  [Rate limits](../../guides/running-workloads#rate-limits).
- Jobs can run side by side, as many as the host has room for. Each
  sandbox is committed its vCPUs and memory while it exists. See
  [Capacity](../../guides/capacity).
- A fork starts in under half a second because the guest reads its memory
  from the snapshot only as it uses it. That needs Cloud Hypervisor v53 or
  later. See
  [How fast a restore is](../../guides/snapshots#how-fast-a-restore-is).
- To change the packages, prepare a new base and take a new snapshot. Do
  the same before upgrading to a release of Dicer that removes the
  [hypervisor version](../../concepts/hypervisors#before-a-version-is-removed)
  that took it, because only that version can fork it.
