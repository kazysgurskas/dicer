// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package token

import (
	"errors"
	"strings"
	"testing"
)

const fingerprint = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestNewSecretsAreValidAndDiffer(t *testing.T) {
	a, b := NewSecret(), NewSecret()
	if a == b {
		t.Fatalf("two secrets are the same: %q", a)
	}
	for _, s := range []string{a, b} {
		if err := ValidateSecret(s); err != nil {
			t.Errorf("ValidateSecret(%q) = %v", s, err)
		}
	}
}

func TestFormatAndParseRoundTrip(t *testing.T) {
	secret := NewSecret()

	for _, want := range []string{fingerprint, ""} {
		token := Format(secret, want)
		if !strings.HasPrefix(token, Prefix) {
			t.Errorf("Format = %q, want it to start with %s", token, Prefix)
		}

		gotSecret, gotFingerprint, err := Parse(token)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", token, err)
		}
		if gotSecret != secret || gotFingerprint != want {
			t.Errorf("Parse(%q) = %q, %q; want %q, %q", token, gotSecret, gotFingerprint, secret, want)
		}
	}
}

// TestParseIgnoresSurroundingSpace checks that a token read from a file or
// pasted with its newline is still read.
func TestParseIgnoresSurroundingSpace(t *testing.T) {
	secret := NewSecret()

	got, _, err := Parse(" " + Format(secret, fingerprint) + "\n")
	if err != nil || got != secret {
		t.Errorf("Parse = %q, %v; want %q", got, err, secret)
	}
}

func TestParseRefusesWhatIsNotAToken(t *testing.T) {
	secret := NewSecret()

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no prefix", secret},
		{"another prefix", "ghp_" + secret},
		{"a short secret", Prefix + "abc"},
		{"a secret with punctuation", Prefix + strings.Repeat("a", 31) + "-"},
		{"a fingerprint too short", Prefix + secret + "_abcdef"},
		{"a fingerprint in capitals", Prefix + secret + "_" + strings.ToUpper(fingerprint)},
		{"a fingerprint not in hex", Prefix + secret + "_" + strings.Repeat("z", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Parse(tt.token); !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%q) = %v, want ErrInvalid", tt.token, err)
			}
		})
	}
}

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		secret string
		valid  bool
	}{
		{strings.Repeat("a", MinSecretLength), true},
		{strings.Repeat("A1", MinSecretLength), true},
		{strings.Repeat("a", MinSecretLength-1), false},
		{strings.Repeat("a", MinSecretLength) + "_", false},
		{strings.Repeat("ä", MinSecretLength), false},
	}
	for _, tt := range tests {
		t.Run(tt.secret, func(t *testing.T) {
			if err := ValidateSecret(tt.secret); (err == nil) != tt.valid {
				t.Errorf("ValidateSecret(%q) = %v, want valid %v", tt.secret, err, tt.valid)
			}
		})
	}
}

func TestSecretSHA256IsHex(t *testing.T) {
	// The SHA-256 of "test".
	if got := SecretSHA256("test"); got != fingerprint {
		t.Errorf("SecretSHA256 = %q", got)
	}
}
