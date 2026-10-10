// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
)

// A bare number says nothing of its unit, so it is refused rather than taken
// as seconds.
func TestExecTimeoutNeedsAUnit(t *testing.T) {
	isolateConfig(t)

	_, err := run(t, "exec", "--timeout", "30", "web", "true")
	if err == nil || !strings.Contains(err.Error(), "want a duration like 30s") {
		t.Errorf("exec --timeout 30 = %v, want it to ask for a duration", err)
	}
}

func TestExecUserIsPassedOn(t *testing.T) {
	cmd := newInstanceExecCommand()
	if err := cmd.ParseFlags([]string{"-u", "postgres:postgres", "db", "psql"}); err != nil {
		t.Fatal(err)
	}

	opts, err := parseExecOptions(cmd)
	if err != nil || opts.user != "postgres:postgres" {
		t.Errorf("parseExecOptions = %+v, %v; want user postgres:postgres", opts, err)
	}
}
