// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/token"
)

func TestRemoteValidate(t *testing.T) {
	value := token.Format(token.NewSecret(), "")

	for _, tc := range []struct {
		name   string
		remote Remote
		want   error
	}{
		{"socket", Remote{Address: "unix:///run/dicer/dicer.sock"}, nil},
		{"host and port", Remote{Address: "192.0.2.1:7443"}, nil},
		{"dns target", Remote{Address: "dns:///dicer1.example.com:7443"}, nil},
		{"with a token", Remote{Address: "192.0.2.1:7443", Token: value}, nil},
		{"relative socket", Remote{Address: "unix://dicer.sock"}, errdefs.ErrInvalidArgument},
		{"no port", Remote{Address: "192.0.2.1"}, errdefs.ErrInvalidArgument},
		{"with what is not a token", Remote{Address: "192.0.2.1:7443", Token: "hunter2"}, errdefs.ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.remote.Validate()
			switch {
			case tc.want == nil && err != nil:
				t.Errorf("Validate = %v, want no error", err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("Validate = %v, want %v", err, tc.want)
			}
		})
	}
}

// A socket is controlled by its file permissions; a token for it would be
// configuration that does nothing, which is worse than being refused.
func TestSocketRemoteRefusesAToken(t *testing.T) {
	err := Remote{Address: "unix:///run/dicer/dicer.sock", Token: token.Format(token.NewSecret(), "")}.Validate()
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "file permissions") {
		t.Errorf("a unix remote with a token = %v, want it refused, saying why", err)
	}
}

func TestIsAddress(t *testing.T) {
	for _, s := range []string{"unix:///run/dicer/dicer.sock", "192.0.2.1:7443", "dns:///host:7443"} {
		if !IsAddress(s) {
			t.Errorf("IsAddress(%q) = false", s)
		}
	}
	for _, s := range []string{"local", "prod", "dicer1.example.com", ""} {
		if IsAddress(s) {
			t.Errorf("IsAddress(%q) = true", s)
		}
	}
}

func TestClientOptions(t *testing.T) {
	if opts := (Remote{Address: "unix:///run/dicer/dicer.sock"}).ClientOptions(); len(opts) != 1 {
		t.Errorf("a socket's options = %d, want its address alone", len(opts))
	}
	if opts := (Remote{Address: "192.0.2.1:7443", Token: token.Format(token.NewSecret(), "")}).ClientOptions(); len(opts) != 2 {
		t.Errorf("a TCP remote's options = %d, want its address and token", len(opts))
	}
}
