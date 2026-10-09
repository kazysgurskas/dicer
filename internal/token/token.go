// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package token makes and reads the tokens a client presents to a daemon's
// TCP listener.
//
// A token is Prefix and a secret, usually followed by an underscore and the
// daemon's fingerprint:
//
//	dicer_<secret>_<fingerprint>
//
// The daemon checks the secret, and keeps only its SHA-256. The client
// checks the fingerprint, the SHA-256 of the daemon's public key, so that
// it never sends the secret to anyone else. A token has no fingerprint when
// the daemon's certificate is one clients verify for themselves. A token
// holds no address: the client says where the daemon is.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Prefix starts every token, so that a token is recognised for what it is,
// by a person or by a secret scanner.
const Prefix = "dicer_"

const (
	// secretBytes is how much randomness a generated secret holds.
	secretBytes = 32

	// MinSecretLength is the fewest characters a secret may have.
	MinSecretLength = 32

	// separator separates a token's secret from its fingerprint. Neither
	// holds one.
	separator = "_"
)

// encoding writes generated secrets in base32. Lowercased, as NewSecret
// does, it holds no separator and survives a shell, a URL and a YAML file
// unquoted.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrInvalid is a string that is not a token, or a secret that cannot be
// one's.
var ErrInvalid = errors.New("invalid token")

// NewSecret returns a new random secret.
func NewSecret() string {
	b := make([]byte, secretBytes)
	_, _ = rand.Read(b) // Read crashes the program rather than return an error.
	return strings.ToLower(encoding.EncodeToString(b))
}

// ValidateSecret returns an error wrapping ErrInvalid unless s can be a
// token's secret: at least MinSecretLength letters and digits.
func ValidateSecret(s string) error {
	if len(s) < MinSecretLength {
		return fmt.Errorf("%w: the secret has %d characters, fewer than %d", ErrInvalid, len(s), MinSecretLength)
	}
	for _, r := range s {
		if !isAlphanumeric(r) {
			return fmt.Errorf("%w: the secret holds %q: want only letters and digits", ErrInvalid, r)
		}
	}
	return nil
}

// isAlphanumeric reports whether r is an ASCII letter or digit.
func isAlphanumeric(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
}

// Format returns the token for a secret and the daemon's fingerprint. An
// empty fingerprint leaves it out.
func Format(secret, fingerprint string) string {
	if fingerprint == "" {
		return Prefix + secret
	}
	return Prefix + secret + separator + fingerprint
}

// Parse returns a token's secret and the fingerprint it holds, if any. It
// returns an error wrapping ErrInvalid if s is not a token.
func Parse(s string) (secret, fingerprint string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), Prefix)
	if !ok {
		return "", "", fmt.Errorf("%w: it does not start with %s", ErrInvalid, Prefix)
	}

	secret, fingerprint, _ = strings.Cut(rest, separator)
	if err := ValidateSecret(secret); err != nil {
		return "", "", err
	}
	if fingerprint != "" && !isFingerprint(fingerprint) {
		return "", "", fmt.Errorf("%w: what follows the secret is not a fingerprint", ErrInvalid)
	}
	return secret, fingerprint, nil
}

// SecretSHA256 returns the SHA-256 of a secret, hex-encoded: what the daemon
// keeps in its place.
func SecretSHA256(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Fingerprint returns the fingerprint of the public key cert holds: its
// SHA-256, hex-encoded. A certificate renewed with the same key keeps it.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// isFingerprint reports whether s is a fingerprint as Fingerprint writes it.
func isFingerprint(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
