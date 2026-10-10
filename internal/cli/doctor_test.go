// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// doctorDaemon is a fake daemon whose host has checks, and whose test
// guests do what guest says.
type doctorDaemon struct {
	*fakeInstanceDaemon

	checks []*dicerdv1.HostCheck
}

func (d *doctorDaemon) CheckHost(context.Context, *dicerdv1.CheckHostRequest) (*dicerdv1.CheckHostResponse, error) {
	return &dicerdv1.CheckHostResponse{Checks: d.checks}, nil
}

// healthyChecks are the host checks of a host every check passes on.
func healthyChecks() []*dicerdv1.HostCheck {
	ok := dicerdv1.HostCheckStatus_HOST_CHECK_STATUS_OK
	return []*dicerdv1.HostCheck{
		{Name: "kvm", Status: ok, Detail: "/dev/kvm is usable"},
		{Name: "ip_forwarding", Status: ok, Detail: "IPv4 forwarding is on"},
	}
}

// guestRuns makes each test guest print console and exit with code once
// started.
func guestRuns(d *fakeInstanceDaemon, console string, code int32) {
	d.onStart = func(name string) {
		d.mu.Lock()
		d.console[name] = console
		d.mu.Unlock()
		go d.stops(name, code)
	}
}

// newDoctorDaemon returns a doctorDaemon whose host carries Cloud
// Hypervisor and Firecracker.
func newDoctorDaemon() *doctorDaemon {
	d := &doctorDaemon{fakeInstanceDaemon: newFakeInstanceDaemon(), checks: healthyChecks()}
	d.host.Hypervisors = []*dicerdv1.HypervisorInfo{
		{Type: dicerdv1.HypervisorType_HYPERVISOR_TYPE_CLOUD_HYPERVISOR, Versions: []string{"v53.0.0"}, IsDefault: true},
		{Type: dicerdv1.HypervisorType_HYPERVISOR_TYPE_FIRECRACKER, Versions: []string{"v1.17.0"}},
	}
	return d
}

func TestDoctorOnAHealthyHost(t *testing.T) {
	d := newDoctorDaemon()
	guestRuns(d.fakeInstanceDaemon, "Linux booting\ndicer-doctor: booted\ndicer-doctor: online\n", 0)
	serveFakeDaemon(t, d)

	out, err := run(t, "doctor")
	if err != nil {
		t.Fatalf("doctor = %v\n%s", err, out)
	}
	for _, want := range []string{
		"✓ KVM", "/dev/kvm is usable",
		"✓ cloud-hypervisor v53.0.0", "✓ firecracker v1.17.0", "booted, ran a command and stopped",
		"✓ Network", "No problems found.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// The test guests are deleted.
	d.mu.Lock()
	left := len(d.instances)
	d.mu.Unlock()
	if left != 0 {
		t.Errorf("%d test guests were left behind", left)
	}
}

func TestDoctorReportsAFailedCheckWithItsHint(t *testing.T) {
	d := newDoctorDaemon()
	d.checks = append(d.checks, &dicerdv1.HostCheck{
		Name: "kvm", Status: dicerdv1.HostCheckStatus_HOST_CHECK_STATUS_FAILED,
		Detail: "/dev/kvm does not exist", Hint: "turn virtualisation on in the firmware",
	})
	serveFakeDaemon(t, d)

	out, err := run(t, "doctor", "--host-only")
	if err == nil || err.Error() != "1 problem found" {
		t.Errorf("doctor = %v, want 1 problem found", err)
	}
	for _, want := range []string{"✗ KVM", "/dev/kvm does not exist", "turn virtualisation on in the firmware", "1 problem found."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Test guests") {
		t.Errorf("--host-only booted test guests:\n%s", out)
	}
}

func TestDoctorReportsAGuestThatDoesNotBoot(t *testing.T) {
	d := newDoctorDaemon()
	guestRuns(d.fakeInstanceDaemon,
		"[    3.659149] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000200\n", 1)
	serveFakeDaemon(t, d)

	out, err := run(t, "doctor")
	if err == nil || err.Error() != "2 problems found" {
		t.Errorf("doctor = %v, want 2 problems found, one for each hypervisor", err)
	}
	for _, want := range []string{"✗ cloud-hypervisor v53.0.0", "ended without running its command", "Kernel panic - not syncing"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorWarnsOfAGuestOffline(t *testing.T) {
	d := newDoctorDaemon()
	guestRuns(d.fakeInstanceDaemon, "dicer-doctor: booted\ndicer-doctor: offline\n", 0)
	serveFakeDaemon(t, d)

	out, err := run(t, "doctor")
	if err != nil {
		t.Fatalf("doctor = %v, want a warning only\n%s", err, out)
	}
	if !strings.Contains(out, "! Network") || !strings.Contains(out, "No problems, 1 warning.") {
		t.Errorf("output does not warn of a guest offline:\n%s", out)
	}
}

func TestDoctorGivesUpOnAGuestThatNeverEnds(t *testing.T) {
	d := newDoctorDaemon()
	d.host.Hypervisors = d.host.GetHypervisors()[:1]
	serveFakeDaemon(t, d)

	out, err := run(t, "doctor", "--timeout", "200ms")
	if err == nil {
		t.Fatalf("doctor succeeded with a guest that never ended:\n%s", out)
	}
	if !strings.Contains(out, "did not run its command within") {
		t.Errorf("output does not say the guest timed out:\n%s", out)
	}
}

func TestDoctorKeepsAFailedGuestWhenAsked(t *testing.T) {
	d := newDoctorDaemon()
	d.host.Hypervisors = d.host.GetHypervisors()[:1]
	guestRuns(d.fakeInstanceDaemon, "no shell here\n", 127)
	serveFakeDaemon(t, d)

	out, _ := run(t, "doctor", "--keep")
	if !strings.Contains(out, "kept as doctor-") {
		t.Errorf("output does not name the kept guest:\n%s", out)
	}
	d.mu.Lock()
	left := len(d.instances)
	d.mu.Unlock()
	if left != 1 {
		t.Errorf("%d test guests left, want the failed one kept", left)
	}
}

func TestDoctorOnADaemonTooOldToCheckItsHost(t *testing.T) {
	d := newFakeInstanceDaemon()
	serveFakeDaemon(t, d)

	start := time.Now()
	out, err := run(t, "doctor", "--host-only")
	if err == nil || !strings.Contains(out, "too old to check its host") {
		t.Errorf("doctor = %v, want the daemon called too old:\n%s", err, out)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("doctor took too long against an old daemon")
	}
}

func TestConsoleExcerptShowsThePanic(t *testing.T) {
	boot := []string{"booting", "Run /init as init process", "Kernel panic - not syncing: Attempted to kill init!", "Rebooting in 1 seconds..", "booting again", "rcu: ..."}
	got := consoleExcerpt(boot)
	if len(got) == 0 || got[len(got)-1] != "Kernel panic - not syncing: Attempted to kill init!" || !slices.Contains(got, "Run /init as init process") {
		t.Errorf("excerpt = %q, want the panic and what led to it", got)
	}

	plain := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := consoleExcerpt(plain); !slices.Equal(got, plain[2:]) {
		t.Errorf("excerpt without a panic = %q, want the last five lines", got)
	}
}

// TestDoctorChecksTheNetworkFromAGuestThatBooted checks that a guest that
// does not boot does not keep the network from being checked.
func TestDoctorChecksTheNetworkFromAGuestThatBooted(t *testing.T) {
	d := newDoctorDaemon()
	// The first guest, Cloud Hypervisor's, panics; the second boots.
	started := 0
	d.onStart = func(name string) {
		d.mu.Lock()
		started++
		console, code := "dicer-doctor: booted\ndicer-doctor: online\n", int32(0)
		if started == 1 {
			console, code = "Kernel panic - not syncing\n", 1
		}
		d.console[name] = console
		d.mu.Unlock()
		go d.stops(name, code)
	}
	serveFakeDaemon(t, d)

	out, _ := run(t, "doctor")
	if !strings.Contains(out, "✗ cloud-hypervisor") || !strings.Contains(out, "✓ Network") {
		t.Errorf("output does not check the network from the guest that booted:\n%s", out)
	}
}
