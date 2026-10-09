// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package health

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/guest"
)

func TestCheckValidate(t *testing.T) {
	tests := []struct {
		name    string
		check   Check
		wantErr bool
	}{
		{name: "exec", check: Check{Exec: []string{"pg_isready"}}},
		{name: "http with path", check: Check{HTTP: &HTTPProbe{Port: 3000, Path: "/api/health"}}},
		{name: "http without path", check: Check{HTTP: &HTTPProbe{Port: 80}}},
		{name: "tcp with timings", check: Check{TCP: &TCPProbe{Port: 5432}, Interval: time.Second, Retries: 1}},
		{name: "disabled", check: Check{Disabled: true}},
		{name: "no probe", check: Check{}, wantErr: true},
		{name: "two probes", check: Check{Exec: []string{"true"}, TCP: &TCPProbe{Port: 1}}, wantErr: true},
		{name: "port zero", check: Check{HTTP: &HTTPProbe{Port: 0}}, wantErr: true},
		{name: "port too high", check: Check{TCP: &TCPProbe{Port: 70000}}, wantErr: true},
		{name: "relative path", check: Check{HTTP: &HTTPProbe{Port: 80, Path: "health"}}, wantErr: true},
		{name: "negative interval", check: Check{Exec: []string{"true"}, Interval: -time.Second}, wantErr: true},
		{name: "negative retries", check: Check{Exec: []string{"true"}, Retries: -1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Validate()
			if tt.wantErr && !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate(%+v) = %v, want an invalid argument error", tt.check, err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate(%+v) = %v, want nil", tt.check, err)
			}
		})
	}
}

func TestEffectiveCheck(t *testing.T) {
	own := &Check{TCP: &TCPProbe{Port: 1}}
	image := &Check{Exec: []string{"true"}}
	disabled := &Check{Disabled: true}

	tests := []struct {
		name     string
		instance *Check
		image    *Check
		want     *Check // nil means none
	}{
		{name: "instance's own wins", instance: own, image: image, want: own},
		{name: "image's when the instance has none", image: image, want: image},
		{name: "instance disables the image's", instance: disabled, image: image},
		{name: "image's disabled", image: disabled},
		{name: "neither"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveCheck(tt.instance, tt.image)
			if tt.want == nil {
				if got != nil {
					t.Errorf("EffectiveHealthCheck() = %v, want none", got)
				}
				return
			}
			if got == nil || got.String() != tt.want.String() {
				t.Errorf("EffectiveHealthCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveCheckFillsInDefaults(t *testing.T) {
	got := EffectiveCheck(nil, &Check{Exec: []string{"true"}})
	if got.Interval != DefaultInterval || got.Timeout != DefaultTimeout ||
		got.StartPeriod != 0 || got.Retries != DefaultRetries {
		t.Errorf("EffectiveHealthCheck() = %+v, want the defaults filled in", got)
	}
}

func TestCheckString(t *testing.T) {
	tests := map[string]Check{
		"exec pg_isready -q":    {Exec: []string{"pg_isready", "-q"}},
		"http :3000/api/health": {HTTP: &HTTPProbe{Port: 3000, Path: "/api/health"}},
		"http :80/":             {HTTP: &HTTPProbe{Port: 80}},
		"tcp :5432":             {TCP: &TCPProbe{Port: 5432}},
		"disabled":              {Disabled: true},
	}

	for want, check := range tests {
		t.Run(want, func(t *testing.T) {
			if got := check.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		})
	}
}

// TestHealthAfter is the rule a run of failures is judged by: a failure inside
// the start period is recorded but does not count, a success counts at once,
// and the retries decide when a workload is unhealthy.
func TestHealthAfter(t *testing.T) {
	check := Check{Retries: 3, StartPeriod: time.Minute}
	at := time.Now()

	for _, tc := range []struct {
		name       string
		results    []Result
		sinceStart time.Duration
		wantStatus Status
		wantStreak int
	}{
		{
			name:       "a fresh check has reached no verdict",
			sinceStart: time.Hour,
			wantStatus: StatusStarting,
		},
		{
			name:       "one pass is healthy",
			results:    []Result{{Healthy: true, At: at}},
			sinceStart: time.Hour,
			wantStatus: StatusHealthy,
		},
		{
			name:       "failures short of the retries are not yet a verdict",
			results:    []Result{{At: at}, {At: at}},
			sinceStart: time.Hour,
			wantStatus: StatusStarting,
			wantStreak: 2,
		},
		{
			name:       "the retries in a row are unhealthy",
			results:    []Result{{At: at}, {At: at}, {At: at}},
			sinceStart: time.Hour,
			wantStatus: StatusUnhealthy,
			wantStreak: 3,
		},
		{
			name:       "failures in the start period do not count",
			results:    []Result{{At: at}, {At: at}, {At: at}, {At: at}},
			sinceStart: time.Second,
			wantStatus: StatusStarting,
		},
		{
			name:       "a pass clears the streak",
			results:    []Result{{At: at}, {At: at}, {Healthy: true, At: at}},
			sinceStart: time.Hour,
			wantStatus: StatusHealthy,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New()
			for _, r := range tc.results {
				got = got.After(check, r, tc.sinceStart)
			}

			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.FailingStreak != tc.wantStreak {
				t.Errorf("failing streak = %d, want %d", got.FailingStreak, tc.wantStreak)
			}
		})
	}
}

// A success in the start period counts at once: a workload that is up is up,
// however long it was given to get there.
func TestHealthAfterTakesASuccessInTheStartPeriod(t *testing.T) {
	got := New().After(Check{Retries: 3, StartPeriod: time.Hour},
		Result{Healthy: true, At: time.Now()}, time.Second)

	if got.Status != StatusHealthy {
		t.Errorf("status = %q, want healthy", got.Status)
	}
}

// What a probe said is kept, bounded, and cut on a rune boundary: the output
// is the guest's to choose, so its size is not to be trusted.
func TestHealthAfterKeepsWhatTheProbeSaid(t *testing.T) {
	at := time.Now()
	got := New().After(Check{Retries: 1},
		Result{Output: strings.Repeat("é", guest.MaxProbeOutput), At: at}, time.Hour)

	if len(got.LastOutput) > guest.MaxProbeOutput {
		t.Errorf("output is %d bytes, want at most %d", len(got.LastOutput), guest.MaxProbeOutput)
	}
	if !utf8.ValidString(got.LastOutput) {
		t.Error("output was cut in the middle of a rune")
	}
	if !got.LastCheck.Equal(at) {
		t.Errorf("last check = %v, want %v", got.LastCheck, at)
	}
}
