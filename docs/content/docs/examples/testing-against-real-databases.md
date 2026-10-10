---
title: Testing against real databases
weight: 4
description: "Give each Go test a real Postgres of its own, forked with the schema already loaded in under a second, and deleted when the test ends."
icon: database
related:
  - /docs/guides/snapshots
  - /docs/guides/using-the-api
  - /docs/reference/go-client
---

Tests of code that uses a database are best run against the real thing,
and each test is best given a database of its own, so that tests can run
side by side without seeing each other's data. Here, each test gets a fork
of a Postgres that is already running, with the schema loaded. The fork is
ready in under a second, and is deleted when the test ends.

The tests run on the Dicer host, as a user in the `dicer` group.

## Snapshot the database

Run Postgres, wait until it accepts connections, load the schema, and take
a snapshot:

```sql {filename="schema.sql"}
CREATE TABLE users (
    id    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL UNIQUE
);
```

```console
$ dicer run -d --name pg-base --vcpus 1 --memory 512MiB \
    -e POSTGRES_PASSWORD=test -l dbtest=postgres postgres:17
$ until dicer exec pg-base pg_isready -q -h 127.0.0.1 -U postgres; do sleep 1; done
$ dicer exec -T pg-base psql -q -v ON_ERROR_STOP=1 -U postgres < schema.sql
$ dicer snapshot create pg-base pg-base
Snapshot pg-base of instance pg-base created in 453ms (memory, 560.3 MiB)
$ dicer rm -f pg-base
```

`pg_isready` asks over TCP, with `-h 127.0.0.1`. While the image sets up
a new database, Postgres listens only on its Unix socket, so a check over
the socket could pass too early. The `dbtest` label lets you find forks
that a test left behind; see [Good to know](#good-to-know).

A fork resumes where the snapshot's guest was, with Postgres running and
the table there:

```console
$ dicer snapshot fork pg-base try-1
Instance try-1 forked from snapshot pg-base in 270ms (172.20.55.218)
$ dicer exec try-1 psql -U postgres -c '\dt'
         List of relations
 Schema | Name  | Type  |  Owner
--------+-------+-------+----------
 public | users | table | postgres
(1 row)
$ dicer rm -f try-1
```

## The helper

This package does the same through the
[Go client](../../guides/using-the-api#from-go), for any test that asks:

```go {filename="dbtest/dbtest.go"}
// Package dbtest gives each test a Postgres database of its own: a fork of
// the pg-base snapshot, deleted when the test ends.
package dbtest

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/konradasb/dicer"
)

// client connects to the local daemon once, for every test in the binary.
var client = sync.OnceValues(func() (*dicer.Client, error) {
	return dicer.NewClient()
})

// Postgres forks a database for t and returns a connection to it. Both are
// closed when t ends.
func Postgres(t testing.TB) *sql.DB {
	t.Helper()

	c, err := client()
	if err != nil {
		t.Fatalf("connect to dicer: %v", err)
	}

	instance, err := c.Snapshots.Fork(t.Context(), "pg-base", dicer.ForkOptions{})
	if err != nil {
		t.Fatalf("fork a database: %v", err)
	}
	t.Cleanup(func() {
		err := c.Instances.Delete(context.Background(), instance.Name, dicer.DeleteOptions{Force: true})
		if err != nil {
			t.Errorf("delete database %s: %v", instance.Name, err)
		}
	})

	db, err := sql.Open("pgx", "postgres://postgres:test@"+instance.IP+"/postgres")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("connect to database %s: %v", instance.Name, err)
	}

	return db
}
```

`Snapshots.Fork` names the fork after the snapshot, such as `pg-base-k3v9`,
and returns once the fork has its own address, which is in `instance.IP`.
Postgres is already accepting connections there. Cleanups
run in reverse order, so the connection is closed before the fork is
deleted. The cleanup uses its own context, because `t.Context()` is
cancelled just before cleanups run.

## Use it

Each test calls `dbtest.Postgres` and gets an empty `users` table. These
two tests run in parallel, and both add a user with the same email, without
a conflict:

```go {filename="users/users_test.go"}
package users_test

import (
	"testing"

	"example.com/shop/dbtest"
)

func TestCreateUser(t *testing.T) {
	t.Parallel()
	db := dbtest.Postgres(t)

	_, err := db.ExecContext(t.Context(), "INSERT INTO users (email) VALUES ($1)", "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("got %d users, want 1", n)
	}
}

func TestDuplicateEmailIsRefused(t *testing.T) {
	t.Parallel()
	db := dbtest.Postgres(t)

	const insert = "INSERT INTO users (email) VALUES ($1)"
	if _, err := db.ExecContext(t.Context(), insert, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), insert, "ada@example.com"); err == nil {
		t.Error("a second user with the same email was created")
	}
}
```

```console
$ go test -v ./users
=== RUN   TestCreateUser
=== PAUSE TestCreateUser
=== RUN   TestDuplicateEmailIsRefused
=== PAUSE TestDuplicateEmailIsRefused
=== CONT  TestCreateUser
=== CONT  TestDuplicateEmailIsRefused
--- PASS: TestCreateUser (0.97s)
--- PASS: TestDuplicateEmailIsRefused (1.00s)
PASS
ok      example.com/shop/users  1.002s
```

Each test's time includes forking its database and deleting it.

## Good to know

- The forks' addresses can be reached only from the Dicer host. Tests on
  another machine can reach the daemon through a
  [remote](../../guides/remote-access), with `dicer.WithAddress` and
  `dicer.WithToken`. To reach the database too, give each fork a host port
  with `ForkOptions.Ports`, and connect to the host on that port instead.
- Each fork is committed the base's vCPUs and memory while it exists, so
  the host's capacity limits how many tests can run at once. `go test
  -parallel` limits how many run at once in each package. A fork the host
  has no room for fails with `dicer.ErrResourceExhausted`. See
  [Capacity](../../guides/capacity).
- A test binary that is killed before its cleanups run, by Ctrl-C or by
  `go test -timeout`, leaves its forks behind. Forks keep the base's
  labels, so `dicer ps --filter label=dbtest` lists them. Delete them with
  `dicer rm -f`.
- To change the schema, delete the snapshot with `dicer snapshot rm
  pg-base` and take it again. You can also load test data before taking
  the snapshot, so that every test starts with it.
- The same helper works for any service that is slow to start, such as
  MySQL, Redis or Elasticsearch. Snapshot it once it is ready, and fork it
  for each test.
- A fork is ready in under a second because the guest reads its memory
  from the snapshot only as it uses it. That needs Cloud Hypervisor v53 or
  later. See
  [How fast a restore is](../../guides/snapshots#how-fast-a-restore-is).
- Only the [hypervisor version](../../concepts/hypervisors#before-a-version-is-removed)
  that took the snapshot can fork it. Before upgrading to a release of
  Dicer that removes it, take the snapshot again.
