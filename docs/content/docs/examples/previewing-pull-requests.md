---
title: Previewing pull requests
weight: 3
description: "Give every pull request a running copy of your app, forked from main in under a second, that sleeps while nobody uses it."
icon: eye
related:
  - /docs/guides/snapshots
  - /docs/guides/standby
  - /docs/guides/remote-access
---

Each pull request gets its own instance, forked from a snapshot of the app
already running main. Opening a preview only checks out the pull request's
changes and restarts the server. An idle preview goes on standby, and the
next request wakes it.

The app here is a Node.js server in `github.com/acme/shop`, listening on
port 3000.

## Snapshot main

Run the app once, and take a memory snapshot of it:

```console
$ dicer run -d --name shop-base --vcpus 2 --memory 1GiB --standby-after 30m \
    node:22 sh -c 'until [ -e /app/server.js ]; do sleep 1; done
                   cd /app; while :; do node server.js; sleep 1; done'
$ dicer exec shop-base git clone -q https://github.com/acme/shop.git /app
$ dicer exec -w /app shop-base npm ci
$ dicer exec --timeout 2m shop-base \
    sh -c 'until curl -fs -o /dev/null localhost:3000; do sleep 1; done'
$ dicer snapshot create shop-base shop-main
Snapshot shop-main of instance shop-base created in 768ms (memory, 1 GiB)
$ dicer rm -f shop-base
```

The command runs the server in a loop, so killing `node` restarts it from
whatever code is in `/app`. Every fork inherits the instance's vCPUs,
memory and `--standby-after`.

## Open and close previews

This script does the rest:

```sh {filename="/usr/local/bin/shop-preview"}
#!/bin/sh
# shop-preview opens and closes the preview of a pull request, and
# refreshes the snapshot of main.
#
#   shop-preview up 482
#   shop-preview down 482
#   shop-preview refresh
set -eu

# deploy checks out a ref in an instance and waits for the restarted server.
deploy() {
	dicer exec -w /app "$1" sh -c "git fetch -q origin $2 &&
		git checkout -q --detach FETCH_HEAD && npm ci --silent && pkill -x node"
	dicer exec --timeout 2m "$1" \
		sh -c 'until curl -fs -o /dev/null localhost:3000; do sleep 1; done'
}

case $1 in
up)
	dicer rm -f --ignore-missing "shop-pr-$2"
	dicer snapshot fork shop-main "shop-pr-$2" -p "$((10000 + $2)):3000"
	deploy "shop-pr-$2" "pull/$2/head"
	;;
down)
	dicer rm -f --ignore-missing "shop-pr-$2"
	;;
refresh)
	dicer snapshot fork shop-main shop-build
	deploy shop-build main
	dicer snapshot delete shop-main
	dicer snapshot create shop-build shop-main
	dicer rm -f shop-build
	;;
esac
```

```console
$ shop-preview up 482
Instance shop-pr-482 forked from snapshot shop-main in 256ms (172.20.171.217)
$ curl http://dicer1.example.com:10482
```

Each preview publishes the server on port 10000 plus its pull request's
number. `up` always starts from a fresh fork, so a new push gets a clean
preview of the latest main. `refresh` brings the snapshot up to date with
main, so that new previews have less to install.

After 30 idle minutes, a preview goes on standby, and its vCPUs and memory
are released. The daemon keeps listening on its port, and the next
connection resumes it where it was, in about 0.3 seconds. Previews that
nobody is looking at cost only disk space: as much as their memory, for the
frozen guest.

## From CI

```yaml {filename=".github/workflows/preview.yaml"}
name: preview

on:
  pull_request:
    types: [opened, reopened, synchronize, closed]
  push:
    branches: [main]

concurrency:
  group: preview-${{ github.event.pull_request.number || 'main' }}

jobs:
  preview:
    runs-on: [self-hosted, dicer]
    env:
      PR: ${{ github.event.pull_request.number }}
    steps:
      - if: github.event_name == 'push'
        run: shop-preview refresh
      - if: github.event.action == 'closed'
        run: shop-preview down "$PR"
      - if: github.event_name == 'pull_request' && github.event.action != 'closed'
        run: |
          shop-preview up "$PR"
          echo "Preview: http://dicer1.example.com:$((10000 + PR))" >> "$GITHUB_STEP_SUMMARY"
```

The runner runs on the Dicer host, as a user in the `dicer` group, which is
as powerful as root. That is why the script lives on the host, where a pull
request cannot change it. A runner elsewhere can use a
[remote](../../guides/remote-access) instead.

## Good to know

- A preview runs the pull request's code with access to the internet.
  Require approval before running workflows for outside contributors.
- Only TCP connections to a published port wake a preview. Connections to
  the host's loopback address don't, so point a reverse proxy at one of the
  host's own addresses instead. See
  [Waking on a connection](../../guides/standby#waking-on-a-connection).
- Keep what the app needs, such as a seeded database, in the same instance.
  Each preview then gets its own copy, and nothing outside it needs waking.
- An app that keeps polling in the background may never look idle. See
  [Automatic standby](../../guides/standby#automatic-standby).
- Only the [hypervisor version](../../concepts/hypervisors#before-a-version-is-removed)
  that took the snapshot can fork it or wake a preview. Before upgrading to
  a release of Dicer that removes it, snapshot main again and reopen the
  previews.
