// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package naming

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
)

// TestValidateAcceptsOnlySubdomainNames checks names are RFC 1123
// subdomains, and so cannot climb out of the directory they are a path in.
func TestValidateAcceptsOnlySubdomainNames(t *testing.T) {
	valid := []string{"web", "web-1", "Web2", "a", "vmlinux-6.1", "v1.2.3", strings.Repeat("a", 63)}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", name, err)
			}
		})
	}

	invalid := []string{
		"", ".", "..", ".hidden", "trailing.", "a..b", "-web", "web-", "db_primary",
		"a/b", "../etc", "has space", strings.Repeat("a", 64) + ".x",
		strings.Repeat("a.", 127) + "a",
	}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate(%q) = %v, want errdefs.ErrInvalidArgument", name, err)
			}
		})
	}
}

// TestValidateHostnameAllowsNoneOrAnRFC1123Name checks that an empty
// hostname, which leaves the guest its default, is accepted, as is any name
// the rule allows, and nothing else.
func TestValidateHostnameAllowsNoneOrAnRFC1123Name(t *testing.T) {
	for _, hostname := range []string{"", "web", "web.example.com"} {
		if err := ValidateHostname(hostname); err != nil {
			t.Errorf("ValidateHostname(%q) = %v, want nil", hostname, err)
		}
	}
	for _, hostname := range []string{"web_1", "-web", "has space", strings.Repeat("a", 254)} {
		if err := ValidateHostname(hostname); !errors.Is(err, errdefs.ErrInvalidArgument) {
			t.Errorf("ValidateHostname(%q) = %v, want errdefs.ErrInvalidArgument", hostname, err)
		}
	}
}

// TestGenerateMakesAValidNameFromAnyBase checks a generated name keeps what
// it can of its base, is valid however unusable the base, and differs each
// time.
func TestGenerateMakesAValidNameFromAnyBase(t *testing.T) {
	for _, tc := range []struct{ base, prefix string }{
		{"web", "web-"},
		{"Web_App", "web-app-"},
		{"vmlinux-6.1", "vmlinux-6-1-"},
		{"web-20260102t150405z", "web-20260102t150405z-"},
		{strings.Repeat("a", 39) + "-b", strings.Repeat("a", 39) + "-"},
		{"___", "instance-"},
		{"", "instance-"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			got := Generate(tc.base)
			if !strings.HasPrefix(got, tc.prefix) || len(got) != len(tc.prefix)+4 {
				t.Errorf("Generate(%q) = %q, want %s and four characters", tc.base, got, tc.prefix)
			}
			if err := Validate(got); err != nil {
				t.Errorf("Generate(%q) = %q, which is invalid: %v", tc.base, got, err)
			}
		})
	}

	if a, b := Generate("web"), Generate("web"); a == b {
		t.Errorf("two names for the same base are both %q", a)
	}
}

// TestGenerateFromImageUsesTheRepositorysLastComponent checks a name made
// from an image keeps neither its registry, path, tag nor digest.
func TestGenerateFromImageUsesTheRepositorysLastComponent(t *testing.T) {
	for _, tc := range []struct{ ref, prefix string }{
		{"nginx", "nginx-"},
		{"docker.io/library/nginx:1.27", "nginx-"},
		{"localhost:5000/team/api-server:v2", "api-server-"},
		{"ghcr.io/acme/Web_App@sha256:0123", "web-app-"},
		{"___", "instance-"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			got := GenerateFromImage(tc.ref)
			if !strings.HasPrefix(got, tc.prefix) || len(got) != len(tc.prefix)+4 {
				t.Errorf("GenerateFromImage(%q) = %q, want %s and four characters", tc.ref, got, tc.prefix)
			}
		})
	}
}
