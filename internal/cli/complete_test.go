// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestCompletionOffersInstancesInTheRightState(t *testing.T) {
	serveFakeDaemon(t, newFakeInstanceDaemon(fakeInstances()...))

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"start", ""}, []string{"db"}},
		{[]string{"stop", ""}, []string{"cache", "web"}},
		{[]string{"unpause", ""}, []string{"cache"}},
		// Names already given are not offered again.
		{[]string{"rm", "web", ""}, []string{"cache", "db"}},
		{[]string{"logs", "web", ""}, nil},
	} {
		out, err := run(t, append([]string{"__complete"}, tc.args...)...)
		if err != nil {
			t.Fatalf("complete %v: %v\n%s", tc.args, err, out)
		}
		if got := completedNames(out); !slices.Equal(got, tc.want) {
			t.Errorf("complete %v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// completedNames picks the values out of cobra's __complete output, which
// ends with a directive line and, here, a note on stderr.
func completedNames(out string) []string {
	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion ended") {
			continue
		}
		names = append(names, completionValue(line))
	}
	return names
}
