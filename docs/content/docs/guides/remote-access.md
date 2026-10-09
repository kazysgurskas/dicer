---
title: Remote access
weight: 9
description: "Serve the API over TCP, and reach it from another machine with a token."
icon: lock-closed
related:
  - /docs/guides/using-the-api
  - /docs/reference/configuration
  - /docs/reference/files-and-environment
---

The daemon serves its API on a Unix socket, for the machine it runs on. It
can also serve the API over TCP, so that `dicer`, the Go package, or any
gRPC client can manage the host from elsewhere. Over TCP, every connection
uses TLS, and every call needs a token.

{{< callout type="warning" >}}
  A token can do anything the daemon can, which is as much as root on the
  host, unless its [scopes](#limit-what-a-token-may-do) say otherwise.
  Guard tokens as you would root SSH keys.
{{< /callout >}}

## On the host itself

The socket, `/run/dicer/dicer.sock`, belongs to root and to the `dicer`
group. The packages and `install.sh` create the group with no members. Use
`sudo` on the host, or join the group and log in again:

```console
$ sudo usermod -aG dicer $USER
$ dicer ps
```

The group's members can do anything with the daemon, which is as much as root
on the host, so add only people you would give root.
[`server.socket.group`](../../reference/configuration#server-socket-group)
in the daemon's configuration names the group. Without it, the socket is
root's alone. The socket needs no token.

## Serve the API over TCP

### 1. Turn the TCP listener on

Set the address to listen on in the daemon's
[configuration](../../reference/configuration#server-listen), and restart
the daemon:

```yaml {filename="/etc/dicerd/config.yaml"}
server:
  listen: 0.0.0.0:7443
```

```console
$ sudo systemctl restart dicerd
$ dicer info
    ...
       Listener: 192.0.2.10:7443
                 fingerprint 3f6c0d…e91a
```

The daemon listens on all of the host's addresses here, so `dicer info`
lists them. The address of the interface the default route uses comes
first.

Restarting the daemon leaves running guests alone. Open the port in the
host's firewall for the addresses clients connect from. The host's own
guests can never reach the port, whatever address it listens on.

The first time it listens, the daemon makes a certificate for itself and
keeps it in its data directory, as `server.crt` and `server.key`. Each
token carries the certificate's fingerprint, so a client checks the daemon
by it. You need no certificate authority, and no DNS name.

### 2. Make a token

On the host, make a token for each person or machine that will connect:

```console
$ dicer token create laptop
dicer_ab3k…
Token laptop created. It is not shown again. To reach this daemon from another machine, run there:

  dicer remote create dicer1 192.0.2.10:7443 --token dicer_ab3k…
```

The token is shown only this once. The daemon keeps only the SHA-256 of
its secret.

The command names the remote after the host, and gives the first address
`dicer info` shows. If the other machine reaches the host at another
address, such as a DNS name, change it before you run the command.

### 3. Create the remote on the client

On the client, run the command `dicer token create` printed:

```console
$ dicer remote create dicer1 192.0.2.10:7443 --token dicer_ab3k…
Remote dicer1 created. Use it with: dicer remote use dicer1
$ dicer --remote dicer1 info
```

`--token` leaves the token in the shell's history. To keep it out, leave
`--token` off and paste the token when asked, where it is not shown. Or
pipe it in, from a password manager or a file:

```console
$ dicer remote create dicer1 192.0.2.10:7443
Token:
$ pass show dicer/dicer1 | dicer remote create dicer1 192.0.2.10:7443
```

Remotes are kept in `remotes.yaml`, which only you can read. It is in
`$DICER_CONFIG_DIR` if that is set. Otherwise it is in `~/.config/dicer` on
Linux (or `$XDG_CONFIG_HOME/dicer`), and in
`~/Library/Application Support/dicer` on macOS.

## Choose which daemon to talk to

Every command goes to one daemon, the first of these that is set:

1. `--remote NAME`, or `-r NAME`, on the command.
2. `$DICER_REMOTE`.
3. The current remote, set with `dicer remote use`.
4. `local`, the daemon on this machine.

```console
$ dicer remote use prod        # from now on, commands go to prod
$ dicer ps
$ DICER_REMOTE=local dicer ps  # this once, the local daemon
$ dicer remote use local       # back to the local daemon
```

`dicer remote list` shows the remotes and which is current. `dicer info`
shows which one a command reaches, and with which token.

`--remote` and `$DICER_REMOTE` also take an address, for a one-off
connection: `unix:///PATH` or `HOST:PORT`. `$DICER_TOKEN` gives the token
for a `HOST:PORT`, and takes the place of a remote's own token. Together
they need no configuration at all, which suits CI:

```console
$ export DICER_REMOTE=dicer1.example.com:7443
$ export DICER_TOKEN=dicer_ab3k…
$ dicer ps
```

Everything works over a remote as it does locally, `exec`, `cp` and
`logs -f` included. A path given to `cp` or `--env-file` is read on the
client, and so is a path given to a `file` mount or to `kernel import`.

## Manage tokens

On the host, or from any client:

```console
$ dicer token list
ID                         NAME     SCOPES   CREATED       LAST USED
c8k2v0x9p4m1q7r3t5w6y8z0   ci       *        2 weeks ago   3 minutes ago
f1h3j5l7n9p2r4t6v8x0z2b4   laptop   *        2 days ago    -
$ dicer token rotate ci        # a new value; the old one stops working at once
$ dicer token delete laptop    # refused from its next call
```

`dicer remote delete` only removes a remote from the client's list. It does
not change what the daemon accepts: delete the token for that.

A tool that must know a token before it exists, such as a configuration
management run, can give the secret itself: at least 32 letters and digits,
on standard input with `--secret-stdin`. `--format json` prints the token
with its details.

## Limit what a token may do

A token made without `--scopes` may do everything, scope `*`. Give a token
only what it needs instead, such as for CI that runs instances from images:

```console
$ dicer token create ci --scopes instances:write,images:write
```

| Scope | Allows |
|---|---|
| `instances:read` | Listing and showing instances, with their logs, stats and processes, and the host's capacity, which shows what each instance holds. |
| `instances:write` | Everything else instances do: creating, starting, stopping, resizing and deleting them, `exec` and `cp`, and restoring or forking a snapshot into one. |
| `snapshots:read`, `snapshots:write` | Listing and showing snapshots; taking and deleting them. |
| `networks:read`, `networks:write` | Listing and showing networks and their addresses; creating and deleting them. |
| `volumes:read`, `volumes:write` | Listing and showing volumes; creating and deleting them. |
| `images:read`, `images:write` | Listing and showing images; pulling, deleting and pruning them. |
| `kernels:read`, `kernels:write` | Listing and showing kernels; importing and deleting them. |
| `tokens:read`, `tokens:write` | Listing and showing tokens; making, rotating and deleting them. |
| `events:read` | `dicer events`: what happens to every resource, so the names of all of them. |

A write scope allows reading too. `exec` and `cp` need `instances:write`
even to read a file, because they reach into the guest. `instances:write`
also lets `dicer run` pull the image an instance needs. Every token may ask
what the daemon is, as `dicer version` and `dicer info` do.

A call the token's scopes do not allow fails, and says which scope it
needs:

```console
$ dicer volume ls
Error: token "ci" lacks scope volumes:read
```

A token with `tokens:write` can make, rotate and delete only tokens whose
scopes its own allow, so it cannot make one that can do more.

## Use a certificate of your own

To have clients check the daemon against a certificate authority instead,
for example with a public certificate for its DNS name, give the daemon the
certificate and its key:

```yaml {filename="/etc/dicerd/config.yaml"}
server:
  listen: 0.0.0.0:7443
  crt_file: /etc/dicerd/tls/server.pem
  key_file: /etc/dicerd/tls/server-key.pem
```

The certificate must name every name and address clients connect to, in its
`subjectAltName`. The daemon reloads the certificate and key when they
change on disk, so renewing them needs no restart.

Tokens made with such a certificate carry no fingerprint. A client checks
the daemon against its host's trusted authorities, and the name it
connects to. For an authority of your own, add it to the client's trusted
authorities, or give it to a Go program with `dicer.WithTLS`.

## Replace the daemon's certificate

If the key of the daemon's own certificate is exposed, stop the daemon,
delete `server.crt` and `server.key` from its data directory, and start it
again. It makes a new certificate, and every token's fingerprint stops
matching it. Clients then fail with a certificate fingerprint mismatch.
Rotate every token with
`dicer token rotate`, and give each client its new value.

## Connections that go quiet

When a daemon's host loses power, or its network drops, nothing closes the
connection, and the client still sees it as open. So a client pings the
daemon every 30 seconds while the connection carries nothing. If no answer
comes within 10 seconds, it gives the connection up and fails the calls on
it, rather than leaving them to wait.

The daemon does the same for its clients, as set by
[`server.keepalive`](../../reference/configuration#server-keepalive). It
lets a client ping as often as every 10 seconds, and disconnects one that
pings more often. A program using the Go package sets its own timings with
`dicer.WithKeepalive`.

A connection over the host's socket is never pinged, because the kernel
closes it if either end goes.
