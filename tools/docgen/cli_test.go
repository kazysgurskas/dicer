// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestFlagUsageQuotingCommandWritesItAsCode checks that a command a flag's
// usage quotes is written as code without its quotes, as it is in a
// command's description.
func TestFlagUsageQuotingCommandWritesItAsCode(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("max-memory", "", "Most memory 'dicer resize' can give the running instance")

	var b bytes.Buffer
	writeFlags(&b, flags, "", map[string]bool{"dicer": true, "dicer resize": true})

	want := "| `--max-memory string` | Most memory `dicer resize` can give the running instance. |"
	if !strings.Contains(b.String(), want) {
		t.Errorf("flags table:\n%s\nwant the row %q", b.String(), want)
	}
}
