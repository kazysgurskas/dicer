// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/konradasb/dicer"
)

// TestPullReporterOffTerminal checks what ends up in a log or a pipe: one
// line per stage, no carriage returns, and no flood of byte counts.
func TestPullReporterOffTerminal(t *testing.T) {
	var out bytes.Buffer
	r := newPullReporter(&out)

	progress := []dicer.PullProgress{
		{Stage: dicer.PullStageResolving},
		{Stage: dicer.PullStageDownloading, TotalBytes: 100},
		{Stage: dicer.PullStageDownloading, DownloadedBytes: 50, TotalBytes: 100},
		{Stage: dicer.PullStageDownloading, DownloadedBytes: 100, TotalBytes: 100},
		{Stage: dicer.PullStageUnpacking},
		{Stage: dicer.PullStageConverting},
	}
	for _, p := range progress {
		r.report(p)
	}
	r.done()

	got := out.String()
	if strings.Contains(got, "\r") {
		t.Error("a redrawn line was written somewhere that is not a terminal")
	}

	want := []string{"Resolving", "Downloading", "Unpacking", "Converting"}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != len(want) {
		t.Fatalf("wrote %d lines (%q), want one per stage", len(lines), got)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestStageLabel(t *testing.T) {
	// An unknown stage still has to read as something.
	if got := stageLabel(""); got == "" {
		t.Error("an unspecified stage has no label")
	}
}
