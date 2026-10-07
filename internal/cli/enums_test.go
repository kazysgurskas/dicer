// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/konradasb/dicer"
)

// TestParseChoiceIgnoresCaseAndSeparators checks that a value is read
// however it is cased, with underscores or hyphens.
func TestParseChoiceIgnoresCaseAndSeparators(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want dicer.RestartMode
	}{
		{"no", dicer.RestartModeNo},
		{"on-failure", dicer.RestartModeOnFailure},
		{"ON_FAILURE", dicer.RestartModeOnFailure},
		{"Unless-Stopped", dicer.RestartModeUnlessStopped},
	} {
		if got, err := parseChoice("restart policy", tc.in, restartModes); err != nil || got != tc.want {
			t.Errorf("parseChoice(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}

	if got, err := parseChoice("--arch", "x86_64", architectures); err != nil || got != dicer.ArchitectureX86_64 {
		t.Errorf("parseChoice(x86_64) = %v, %v", got, err)
	}
}

// TestParseChoiceRejectsWhatIsNotAChoice checks that anything else fails,
// naming the choices.
func TestParseChoiceRejectsWhatIsNotAChoice(t *testing.T) {
	for _, s := range []string{"", "unspecified", "sometimes"} {
		_, err := parseChoice("restart policy", s, restartModes)
		if err == nil {
			t.Errorf("parseChoice(%q) should fail", s)
			continue
		}
		if want := "want no, on-failure, unless-stopped or always"; !strings.Contains(err.Error(), want) {
			t.Errorf("parseChoice(%q) = %v, want it to say %q", s, err, want)
		}
	}
}
