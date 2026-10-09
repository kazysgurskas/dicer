// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon())

	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	for _, want := range []string{"Client:", "Server:", "v9.9.9", "compute-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output is missing %q:\n%s", want, out)
		}
	}
}

func TestVersionWithoutDaemon(t *testing.T) {
	isolateConfig(t)
	t.Setenv(remoteEnv, "unix:///nonexistent/dicer.sock")

	out, err := run(t, "version")
	if err == nil {
		t.Error("version should fail when the daemon cannot be reached")
	}
	if !strings.Contains(out, "Client:") || strings.Contains(out, "Server:") {
		t.Errorf("the client's version should still be shown:\n%s", out)
	}
}
