// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLifecycleShortcutsTakeManyNames(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	if out, err := run(t, "stop", "web", "cache"); err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if out, err := run(t, "start", "web", "cache", "db"); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if out, err := run(t, "pause", "web"); err != nil {
		t.Fatalf("pause: %v\n%s", err, out)
	}
	if out, err := run(t, "unpause", "web"); err != nil {
		t.Fatalf("unpause: %v\n%s", err, out)
	}

	want := []string{
		"stop web", "stop cache",
		"start web", "start cache", "start db",
		"pause web", "resume web",
	}
	if !slices.Equal(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
}

// TestRmCarriesOnPastAFailure pins down docker rm's behaviour: one missing
// name is reported, the rest are still deleted, and the command fails.
func TestRmCarriesOnPastAFailure(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	out, err := run(t, "rm", "-f", "web", "nope", "db")

	var exitErr *exitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("err = %v, want exit status 1", err)
	}
	if !strings.Contains(out, `no instance "nope"`) {
		t.Errorf("output should report the missing instance:\n%s", out)
	}
	if want := []string{"delete web", "delete db"}; !slices.Equal(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
}

func TestRestart(t *testing.T) {
	d := newFakeInstanceDaemon(fakeInstances()...)
	serveFakeDaemon(t, d)

	if out, err := run(t, "restart", "web", "db"); err != nil {
		t.Fatalf("restart: %v\n%s", err, out)
	}

	// A running instance is stopped first; a stopped one only started.
	if want := []string{"stop web", "start web", "start db"}; !slices.Equal(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
}
