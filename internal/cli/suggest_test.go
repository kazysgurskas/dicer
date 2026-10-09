// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestDidYouMean(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	for _, args := range [][]string{{"stop", "wbe"}, {"inspect", "weeb"}} {
		_, err := run(t, args...)
		if err == nil || !strings.HasSuffix(err.Error(), " (did you mean web?)") {
			t.Errorf("%v = %v, want a suggestion of web", args, err)
		}
	}

	// Nothing close: no suggestion.
	if _, err := run(t, "stop", "zzzzzz"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("stop zzzzzz = %v, want a plain not found", err)
	}
}

func TestCloseNames(t *testing.T) {
	names := []string{"web", "web-2", "db", "database", "cache"}
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"wbe", []string{"web"}},
		{"web", []string{"web-2"}},
		{"dtabase", []string{"database"}},
		{"cahce", []string{"cache"}},
		{"data", []string{"database"}},
		{"xyz", nil},
	} {
		if got := closeNames(tc.name, names); !slices.Equal(got, tc.want) {
			t.Errorf("closeNames(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
