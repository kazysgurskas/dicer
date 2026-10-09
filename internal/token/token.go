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
	"slices"
	"strings"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
)

// Scope is what a token allows: ScopeAll, or an action on a kind of
// resource, written RESOURCE:ACTION. A read scope allows the calls that only
// look at its resource. A write scope also allows those that change it.
type Scope string

// ScopeAll allows everything, now and as the API grows.
const ScopeAll Scope = "*"

// The scopes of each kind of resource. Events, the log of what happens to
// every other resource, can only be read.
const (
	ScopeInstancesRead  Scope = "instances:read"
	ScopeInstancesWrite Scope = "instances:write"
	ScopeSnapshotsRead  Scope = "snapshots:read"
	ScopeSnapshotsWrite Scope = "snapshots:write"
	ScopeNetworksRead   Scope = "networks:read"
	ScopeNetworksWrite  Scope = "networks:write"
	ScopeVolumesRead    Scope = "volumes:read"
	ScopeVolumesWrite   Scope = "volumes:write"
	ScopeImagesRead     Scope = "images:read"
	ScopeImagesWrite    Scope = "images:write"
	ScopeKernelsRead    Scope = "kernels:read"
	ScopeKernelsWrite   Scope = "kernels:write"
	ScopeTokensRead     Scope = "tokens:read"
	ScopeTokensWrite    Scope = "tokens:write"
	ScopeEventsRead     Scope = "events:read"
)

// resourceScopes are the scopes of each kind of resource, as the constants
// above name them.
var resourceScopes = []Scope{
	ScopeInstancesRead, ScopeInstancesWrite,
	ScopeSnapshotsRead, ScopeSnapshotsWrite,
	ScopeNetworksRead, ScopeNetworksWrite,
	ScopeVolumesRead, ScopeVolumesWrite,
	ScopeImagesRead, ScopeImagesWrite,
	ScopeKernelsRead, ScopeKernelsWrite,
	ScopeTokensRead, ScopeTokensWrite,
	ScopeEventsRead,
}

// Validate returns an invalid argument error unless s is ScopeAll or the
// scope of a kind of resource.
func (s Scope) Validate() error {
	if s == ScopeAll || slices.Contains(resourceScopes, s) {
		return nil
	}

	valid := make([]string, 0, len(resourceScopes))
	for _, scope := range resourceScopes {
		valid = append(valid, string(scope))
	}
	return errdefs.InvalidArgument("invalid scope %q: want %s, or one of %s",
		s, ScopeAll, strings.Join(valid, ", "))
}

// Token is a credential a client presents to the daemon's TCP listener. The
// daemon keeps the SHA-256 of its secret, never the secret itself.
type Token struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`

	// SecretSHA256 is the SHA-256 of the token's secret, hex-encoded.
	SecretSHA256 string `yaml:"secret_sha256"`

	// Scopes are what the token allows.
	Scopes []Scope `yaml:"scopes"`

	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`

	// LastUsedAt is when the token last made a call, to the minute. It is
	// zero for a token never used.
	LastUsedAt time.Time `yaml:"last_used_at,omitempty"`
}

// Validate returns an invalid argument error unless the token has a valid
// name, a secret's SHA-256 and at least one scope, each of them valid and
// none repeated.
func (t Token) Validate() error {
	if err := naming.Validate(t.Name); err != nil {
		return err
	}
	if !isSHA256Hex(t.SecretSHA256) {
		return errdefs.InvalidArgument("token %q has no secret's SHA-256", t.Name)
	}

	if len(t.Scopes) == 0 {
		return errdefs.InvalidArgument("a token needs at least one scope: %s for everything", ScopeAll)
	}
	for i, scope := range t.Scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		if slices.Contains(t.Scopes[:i], scope) {
			return errdefs.InvalidArgument("scope %q is given twice", scope)
		}
	}
	if len(t.Scopes) > 1 && slices.Contains(t.Scopes, ScopeAll) {
		return errdefs.InvalidArgument("scope %s allows everything already: give it alone", ScopeAll)
	}
	return nil
}

// Allows reports whether the token's scopes allow scope: it has ScopeAll,
// scope itself, or, for a read scope, the write scope of the same resource.
func (t Token) Allows(scope Scope) bool {
	if slices.Contains(t.Scopes, ScopeAll) || slices.Contains(t.Scopes, scope) {
		return true
	}
	resource, isRead := strings.CutSuffix(string(scope), ":read")
	return isRead && slices.Contains(t.Scopes, Scope(resource+":write"))
}

// AllowsAll reports whether the token's scopes allow every one of scopes,
// so that a token with them could do nothing this one cannot.
func (t Token) AllowsAll(scopes []Scope) bool {
	for _, scope := range scopes {
		if !t.Allows(scope) {
			return false
		}
	}
	return true
}

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

// isSHA256Hex reports whether s is a hex-encoded SHA-256 digest.
func isSHA256Hex(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}
