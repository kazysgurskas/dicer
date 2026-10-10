// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRunExitsWithItsWorkloadsStatus runs a job in the foreground, as a CI
// step would: dicer prints what the job prints, exits with the status it
// exited with, and --rm deletes it.
//
// The job ends as soon as it boots, so it is gone before dicer could ask
// about it afterwards: only a wait that began before it started hears how it
// ended.
func TestRunExitsWithItsWorkloadsStatus(t *testing.T) {
	name := instanceName(t)
	t.Cleanup(func() { env.deleteInstance(t, name) })

	out, err := env.tryDicer(t, "run", "--rm", "--name", name, "--memory", "512MiB",
		testImage, "sh", "-c", "echo from-the-job; exit 3")
	if code := exitCode(err); code != 3 {
		t.Fatalf("dicer run exited with %d (%v), want the job's 3", code, err)
	}
	if !strings.Contains(out, "from-the-job") {
		t.Errorf("dicer run printed:\n%s\nwant what the job printed", out)
	}
	if _, err := env.tryDicer(t, "instance", "show", name); !isNotFound(err) {
		t.Errorf("show after the job ended = %v, want it deleted by --rm", err)
	}
}

// TestWaitReportsHowAnInstanceEnded waits for an instance running in the
// background, and then for it again once it has stopped.
func TestWaitReportsHowAnInstanceEnded(t *testing.T) {
	name := instanceName(t)
	env.dicer(t, "run", "-d", "--name", name, "--memory", "512MiB", testImage, "sh", "-c", "sleep 3; exit 4")
	t.Cleanup(func() { env.deleteInstance(t, name) })

	for _, when := range []string{"while it runs", "once it has stopped"} {
		if got := strings.TrimSpace(env.dicer(t, "wait", name)); got != "4" {
			t.Errorf("dicer wait %s printed %q, want 4", when, got)
		}
	}
}

// TestWaitForADeletedJobIsNotFound checks that a job run with --rm, which
// ended and was deleted before the wait began, cannot be waited for: nothing
// is left to say how it ended.
func TestWaitForADeletedJobIsNotFound(t *testing.T) {
	name := instanceName(t)
	env.dicer(t, "run", "-d", "--rm", "--name", name, "--memory", "512MiB", testImage, "sh", "-c", "exit 5")
	t.Cleanup(func() { env.deleteInstance(t, name) })

	deadline := time.Now().Add(time.Minute)
	for {
		_, err := env.tryDicer(t, "instance", "show", name)
		if isNotFound(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job was not deleted as it ended: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if out, err := env.tryDicer(t, "wait", name); !isNotFound(err) {
		t.Errorf("dicer wait = %q, %v; want the job not found", out, err)
	}
}

// exitCode returns the status a command run on the host exited with, 0 if
// it succeeded, or -1 if it did not run.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}
