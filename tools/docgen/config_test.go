// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import "testing"

func TestAsCodeWritesWholeWordsAsCode(t *testing.T) {
	commands := map[string]bool{"dicer": true, "dicer resize": true, "dicer compose up": true}
	tests := []struct {
		name string
		text string
		want string
	}{
		{"flag", "set --url", "set `--url`"},
		{"flag with digits", "verified against --sha256 if given", "verified against `--sha256` if given"},
		{"command", "see dicer resize", "see `dicer resize`"},
		{"command and its flag", "dicer compose up --pull", "`dicer compose up --pull`"},
		{"code left alone", "already `--sha256`", "already `--sha256`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := asCode(tt.text, nil, commands); got != tt.want {
				t.Errorf("asCode(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
