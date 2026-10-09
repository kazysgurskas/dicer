---
title: Sandboxing Claude Code
weight: 5
description: "Run Claude Code without permission prompts on a Dicer host, from your own machine, and get its changes back as edits to review."
icon: sparkles
related:
  - /docs/guides/remote-access
  - /docs/guides/snapshots
  - /docs/reference/go-client
---

With `--dangerously-skip-permissions`, Claude Code works through a task on
its own, without asking before each command. That is best done somewhere
it can do no harm. This page builds `claude-sandbox`, a short Go program
you run on your laptop, in a Git repository, with a task. It gives the task
a machine of its own on a Dicer host, and brings Claude's changes back to
your working tree.

## How it works

For each task, `claude-sandbox`:

1. forks a sandbox from a snapshot of a machine with Claude Code installed,
   in about a second;
2. copies in the files of your last commit;
3. runs Claude on the task
4. applies Claude's changes to your working tree, neither staged nor
   committed;
5. deletes the sandbox

Claude can reach nothing of yours but that copy. The sandbox gets no Git
history, so none of your remotes, and no ignored or untracked files, such
as `.env`.

Your key for Claude reaches only the command that runs it, and
is never written to the snapshot.

## Set it up

### On the Dicer host

Make the snapshot. Start from an image with your project's toolchain, here
Go, add a user for Claude, and install Claude Code as that user:

```console
$ dicer run -d --name claude-base --vcpus 2 --memory 2GiB golang:1.25 sleep infinity
$ dicer exec claude-base sh -c 'useradd -m claude &&
    runuser -u claude -- sh -c "curl -fsSL https://claude.ai/install.sh | bash"'
$ dicer snapshot create claude-base claude-base
Snapshot claude-base of instance claude-base created in 2.3s (memory, 2.7 GiB)
$ dicer rm -f claude-base
```

Claude Code refuses to skip permissions as root, which is why it gets a
user of its own. Every sandbox gets the base's 2 vCPUs and 2 GiB of memory.

Then make a token for your laptop:

```console
$ dicer token create claude-sandbox --scopes instances:write,events:read
```

`instances:write` lets the token fork the snapshot, work in the fork and
delete it, and `events:read` lets you look back at what happened. The
command prints the token, once. The host must serve the API over TCP; see
[Remote access](../../guides/remote-access).

### On your laptop

Build [the program](#the-program) with `go build`. Then give it, and
`dicer`, the host's address and the token, and give Claude a key:

```console
$ export DICER_REMOTE=dicer1.example.com:7443
$ export DICER_TOKEN=dicer_…
$ export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-…
```

`CLAUDE_CODE_OAUTH_TOKEN` takes a token for a Claude subscription, which
`claude setup-token` makes. Set `ANTHROPIC_API_KEY` instead to use an API
key from the [Claude Console](https://platform.claude.com/settings/keys).

## Run a task

The tests of this small Go module fail:

```console
$ go test ./slug
--- FAIL: TestMake (0.00s)
    --- FAIL: TestMake/__Espresso_Cups__ (0.00s)
        slug_test.go:18: Make("  Espresso Cups  ") = "--espresso-cups--", want "espresso-cups"
    --- FAIL: TestMake/Milk__Frother (0.00s)
        slug_test.go:18: Make("Milk  Frother") = "milk--frother", want "milk-frother"
    --- FAIL: TestMake/Crème_Brûlée_Torch (0.00s)
        slug_test.go:18: Make("Crème Brûlée Torch") = "crme-brle-torch", want "creme-brulee-torch"
FAIL
FAIL	example.com/shop/slug	0.319s
FAIL
```

Give Claude the task:

```console
$ claude-sandbox "make the failing tests in ./slug pass"
Claude is working in claude-sfm26t
The failing tests in `./slug` now pass; I ran `go test ./...` and it reports `ok`.

The fix is in `slug/slug.go`. `Make` had two bugs:
- **Separators:** every space became its own hyphen, so leading, trailing and repeated spaces gave `--espresso-cups--` and `milk--frother`. Any run of non-alphanumeric characters now becomes a single hyphen, and none are added at the start or end. This also keeps `Moka Pot (6 cups)` at `moka-pot-6-cups`.
- **Accents:** accented letters were dropped, so `Crème Brûlée` became `crme-brle`. I added a small table that maps common accented Latin letters to ASCII, plus `ß`, `æ` and `œ`.

The table covers common Western European letters only, because the module has no dependencies and I didn't add one. Other scripts and Latin letters outside the table, such as `ł` or `ş`, are treated as separators. For wider coverage you could switch to `golang.org/x/text` with NFD normalization.
```

Fifty-two seconds later, the fix is in the working tree, ready to review
with `git diff` and to keep or discard as you would your own:

```console
$ git status --short
 M slug/slug.go
$ go test ./slug
ok  	example.com/shop/slug	0.166s
```

## Watch a run

`dicer` reads the same two variables, so it sees the sandbox on the host
while Claude works:

```console
$ dicer ps --wide
NAME           IMAGE                           STATE    STATUS         VCPU  MEMORY   DISK    NETWORK  IP              PORTS  CREATED
claude-sfm26t  docker.io/library/golang:1.25   Running  Up 12 seconds  2     2 GiB    10 GiB  default  172.20.86.242   -      13 seconds ago
```

Inside it, Claude runs as `claude`, and is building the tests:

```console
$ dicer top claude-sfm26t
PID   PPID  USER    STATE  STARTED         CPUTIME  RSS        COMMAND
1     0     root    S      30 seconds ago  410ms    18.6 MiB   /init
644   1     root    S      29 seconds ago  70ms     15 MiB     /usr/local/bin/dicer-agent
650   1     root    S      29 seconds ago  0s       1.4 MiB    sleep infinity
726   644   root    S      12 seconds ago  0s       3.3 MiB    runuser -u claude -- /home/claude/.local/bin/claude -p make the failing tests in ./slug pass --dangerously-skip-permissions
727   726   claude  S      12 seconds ago  2.96s    231.3 MiB  /home/claude/.local/bin/claude -p make the failing tests in ./slug pass --dangerously-skip-permissions
…
784   782   claude  S      6 seconds ago   870ms    21.2 MiB   go test ./...
1047  784   claude  R      5 seconds ago   6.67s    209.7 MiB  /usr/local/go/pkg/tool/linux_amd64/compile -o /tmp/go-build471559153/b010/_pkg_.a …
```

Once Claude is done, the sandbox is gone.

## The program

```go {filename="main.go"}
// Command claude-sandbox runs Claude Code on a task in a sandbox on a Dicer
// host, and applies Claude's changes to the Git working tree it is run in.
//
//	claude-sandbox "make the failing tests in ./slug pass"
//
// The sandbox is a fork of the claude-base snapshot, given the files of the
// repository's last commit, and deleted when Claude is done. $DICER_REMOTE
// and $DICER_TOKEN say which host, as they do for dicer. Claude
// authenticates with $ANTHROPIC_API_KEY or $CLAUDE_CODE_OAUTH_TOKEN.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/konradasb/dicer"
)

func main() {
	log.SetFlags(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, strings.Join(os.Args[1:], " ")); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, task string) error {
	client, err := dicer.NewClient(
		dicer.WithAddress(os.Getenv("DICER_REMOTE")),
		dicer.WithToken(os.Getenv("DICER_TOKEN")),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	name := "claude-" + strings.ToLower(rand.Text()[:6])
	if _, err := client.Snapshots.Fork(ctx, "claude-base", dicer.ForkOptions{Name: name}); err != nil {
		return err
	}
	defer client.Instances.Delete(context.WithoutCancel(ctx), name, dicer.DeleteOptions{Force: true})
	log.Printf("Claude is working in %s", name)

	// The sandbox gets the last commit's files, in a repository of its own
	// whose one commit is tagged base.
	files, err := exec.Command("git", "archive", "--prefix=work/", "HEAD^{tree}").Output()
	if err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	if err := client.Instances.CopyArchiveTo(ctx, name, "/home/claude", bytes.NewReader(files)); err != nil {
		return err
	}
	setup := client.Instances.Command(name, "sh", "-c",
		"git init -q && git add -A && git -c user.name=sandbox -c user.email=sandbox@localhost commit -qm base && "+
			"git tag base && chown -R claude: .")
	setup.Dir = "/home/claude/work"
	if _, err := setup.Output(ctx); err != nil {
		return err
	}

	// Claude Code skips permissions only for a user other than root.
	claude := client.Instances.Command(name, "runuser", "-u", "claude", "--",
		"/home/claude/.local/bin/claude", "-p", task, "--dangerously-skip-permissions")
	claude.Dir = "/home/claude/work"
	claude.Env = map[string]string{}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value := os.Getenv(key); value != "" {
			claude.Env[key] = value
		}
	}
	claude.Stdout, claude.Stderr = os.Stdout, os.Stderr
	if err := claude.Run(ctx); err != nil {
		return err
	}

	// Claude's changes are what differs from base, its commits included.
	diff := client.Instances.Command(name, "runuser", "-u", "claude", "--",
		"sh", "-c", "git add -A && git diff --cached --binary base")
	diff.Dir = "/home/claude/work"
	patch, err := diff.Output(ctx)
	if err != nil {
		return err
	}
	apply := exec.Command("git", "apply", "--allow-empty")
	apply.Stdin = bytes.NewReader(patch)
	apply.Stderr = os.Stderr

	return apply.Run()
}
```

The sandbox gets the files of your last commit, so commit or stash what
Claude should see first. Its copy is a repository of its own, whose one
commit is tagged `base`, so Claude's changes are what differs from `base`,
including anything it commits.

To make it your own:

- **Another language.** Start the base from an image with its toolchain,
  such as `node:22` or `python:3.13`.
- **More direction.** Add Claude Code's own flags to its command, such as
  `--model` or `--max-turns`.
- **More at once.** Each run has a sandbox of its own, so several can run
  side by side, as many as the host has room for. See
  [Capacity](../../guides/capacity).
