// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
	"time"
)

func TestDebugTracesCalls(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	out, err := run(t, "--debug", "ps", "-q")
	if err != nil {
		t.Fatalf("ps: %v\n%s", err, out)
	}
	for _, want := range []string{"debug ", "remote unix://", "ListInstances OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("--debug output is missing %q:\n%s", want, out)
		}
	}

	t.Setenv(debugEnv, "1")
	if out, _ := run(t, "ps", "-q"); !strings.Contains(out, "ListInstances OK") {
		t.Errorf("$%s should trace as --debug does:\n%s", debugEnv, out)
	}
}

func TestRequestTimeout(t *testing.T) {
	d := newFakeInstanceDaemon()
	d.hostDelay = time.Second
	serveFakeDaemon(t, d)

	_, err := run(t, "--request-timeout", "50ms", "info")
	if err == nil || !strings.Contains(err.Error(), "GetHostInfo took longer than --request-timeout 50ms") {
		t.Errorf("err = %v, want it to name the call and the timeout", err)
	}
}

func TestNegativeTimeoutsAreRefused(t *testing.T) {
	isolateConfig(t)

	for _, args := range [][]string{
		{"exec", "--timeout", "-1s", "web", "true"},
		{"wait", "--timeout", "-1s", "web"},
		{"--request-timeout", "-1s", "info"},
	} {
		_, err := run(t, args...)
		if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
			t.Errorf("%v = %v, want it refused as negative", args, err)
		}
	}
}
