// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestPsWatch(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"ps", "-q", "--watch", "--interval", "200ms"})

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("ps --watch: %v", err)
	}

	// Off a terminal, each redraw follows the last after a blank line.
	frames := strings.Split(strings.TrimSpace(out.String()), "\n\n")
	if len(frames) < 2 {
		t.Fatalf("got %d frames, want at least 2:\n%s", len(frames), out.String())
	}
	for _, f := range frames {
		if f != "cache\ndb\nweb" {
			t.Errorf("frame = %q", f)
		}
	}

	if _, err := run(t, "ps", "--watch", "--interval", "1ms"); err == nil {
		t.Error("an interval that would hammer the daemon should be refused")
	}
}
