// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package naming is the rule every name Dicer gives a resource follows: an
// instance's, a network's, a snapshot's, a remote's; and a guest's
// hostname, which follows the same rule. It also makes up names that follow
// it.
package naming

import (
	"crypto/rand"
	"regexp"
	"strings"

	"github.com/konradasb/dicer/internal/errdefs"
)

// namePattern matches an RFC 1123 subdomain. Names become paths under the
// data directory, so this also rules out ".." and leading dots.
var namePattern = regexp.MustCompile(
	`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// maxNameLength is the longest a name may be, as for a hostname.
const maxNameLength = 253

// nameRule says what namePattern allows.
const nameRule = "use letters, digits and hyphens, in dot-separated parts that each start and end with a letter or digit"

// Validate returns an error in the errdefs.ErrInvalidArgument class if name
// is not a valid resource name, such as "vmlinux-6.1", and nil if it is.
func Validate(name string) error {
	if len(name) > maxNameLength || !namePattern.MatchString(name) {
		return errdefs.InvalidArgument("invalid name %q: %s", name, nameRule)
	}

	return nil
}

// ValidateHostname returns an error in the errdefs.ErrInvalidArgument class
// if hostname is set and is not valid under RFC 1123, and nil if it is or
// is empty.
func ValidateHostname(hostname string) error {
	switch {
	case hostname == "":
		return nil
	case len(hostname) > maxNameLength:
		return errdefs.InvalidArgument("invalid hostname %q: it exceeds %d characters", hostname, maxNameLength)
	case !namePattern.MatchString(hostname):
		return errdefs.InvalidArgument("invalid hostname %q: %s", hostname, nameRule)
	}
	return nil
}

// notNameChars are what a generated name cannot have, and are replaced
// with hyphens.
var notNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

// maxGeneratedBaseLength is the most of its base a generated name keeps.
const maxGeneratedBaseLength = 40

// generatedSuffixChars are what a generated name's suffix is drawn from.
const generatedSuffixChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// Generate returns a new valid name made of base and a random suffix, such
// as web-k3x9 for web. Base is lowercased and shortened, and characters
// such as dots become hyphens. A base with nothing usable left gives a name
// such as instance-k3x9.
func Generate(base string) string {
	base = strings.Trim(notNameChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if len(base) > maxGeneratedBaseLength {
		base = strings.TrimRight(base[:maxGeneratedBaseLength], "-")
	}
	if base == "" {
		base = "instance"
	}

	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	for i, b := range suffix {
		suffix[i] = generatedSuffixChars[int(b)%len(generatedSuffixChars)]
	}

	return base + "-" + string(suffix)
}

// GenerateFromImage returns a new name made from an image reference's last
// path component, as Generate does from a base: nginx-k3x9 for
// docker.io/library/nginx:1.27.
func GenerateFromImage(imageRef string) string {
	base, _, _ := strings.Cut(imageRef, "@")
	base = base[strings.LastIndexByte(base, '/')+1:]
	base, _, _ = strings.Cut(base, ":")

	return Generate(base)
}
