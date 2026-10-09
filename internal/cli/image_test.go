// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
)

func TestImagePullReportsUpToDate(t *testing.T) {
	d := newFakeInstanceDaemon()
	serveFakeDaemon(t, d)

	if out, err := run(t, "pull", "nginx:1.27"); err != nil || !strings.Contains(out, "Image nginx:1.27 pulled in") ||
		!strings.Contains(out, "sha256:0123456789ab,") {
		t.Errorf("first pull = %q, %v", out, err)
	}
	if out, err := run(t, "pull", "nginx:1.27"); err != nil || !strings.Contains(out, "Image nginx:1.27 is up to date") {
		t.Errorf("second pull = %q, %v", out, err)
	}
}
