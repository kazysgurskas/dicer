// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"maps"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/health"
	"github.com/konradasb/dicer/internal/image"
)

// quickCheck is a check that reaches a verdict in milliseconds.
var quickCheck = health.Check{
	Exec:     []string{"true"},
	Interval: 5 * time.Millisecond,
	Timeout:  5 * time.Millisecond,
	Retries:  2,
}

// monitored returns a harness whose instance is checked with quickCheck,
// probed by the returned fake.
func monitored(t *testing.T, restart RestartPolicy, healthy bool) (*harness, *fakeProbe) {
	t.Helper()

	h := newHarness(t)
	probe := &fakeProbe{healthy: healthy}
	h.manager.probe = probe.probe

	check := quickCheck
	h.instance.HealthCheck = &check
	h.setRestart(t, restart)
	return h, probe
}

// waitForHealth polls until the instance's health is want.
func (h *harness) waitForHealth(t *testing.T, want health.Status) health.Health {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, got, ok := h.manager.Health(h.instance); ok && got.Status == want {
			return got
		}
		if time.Now().After(deadline) {
			_, got, ok := h.manager.Health(h.instance)
			t.Fatalf("health = %+v (monitored: %v), want %s", got, ok, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHealthyInstanceIsReportedHealthy(t *testing.T) {
	h, _ := monitored(t, RestartPolicy{}, true)
	h.start(t)

	got := h.waitForHealth(t, health.StatusHealthy)
	if got.LastOutput != "ok" || got.LastCheck.IsZero() {
		t.Errorf("health = %+v, want the last probe's output and time", got)
	}

	check, _, _ := h.manager.Health(h.instance)
	if check.String() != "exec true" {
		t.Errorf("check = %s, want the instance's", check)
	}
}

// Usage counts checked instances by verdict, naming every verdict.
func TestUsageCountsHealth(t *testing.T) {
	h, _ := monitored(t, RestartPolicy{}, true)
	h.start(t)
	h.waitForHealth(t, health.StatusHealthy)

	got := h.manager.Usage().ByHealth
	want := map[health.Status]int{health.StatusStarting: 0, health.StatusHealthy: 1, health.StatusUnhealthy: 0}
	if !maps.Equal(got, want) {
		t.Errorf("ByHealth = %v, want %v", got, want)
	}
}

// Unhealthy is a failure to the restart policy: the VM is stopped and the
// policy restarts it.
func TestUnhealthyInstanceIsRestarted(t *testing.T) {
	h, probe := monitored(t, RestartPolicy{Mode: RestartModeAlways}, false)
	h.restartAtOnce()
	h.start(t)
	first := h.starter.vmm()

	h.waitForVMMs(t, 2)
	probe.set(true, nil)
	status := h.waitForState(t, StateRunning)

	select {
	case <-first.Done():
	default:
		t.Error("the unhealthy instance's VMM is still running")
	}
	if status.RestartCount != 1 {
		t.Errorf("restart count = %d, want 1", status.RestartCount)
	}
	if n := h.hostNetwork.cancelledTeardowns.Load(); n > 0 {
		t.Errorf("%d network teardowns were asked for with a cancelled context", n)
	}
	h.waitForHealth(t, health.StatusHealthy)
}

// Under the policy no, an unhealthy instance is reported and left running:
// stopping it would only make things worse.
func TestUnhealthyInstanceWithoutRestartPolicyKeepsRunning(t *testing.T) {
	h, _ := monitored(t, RestartPolicy{Mode: RestartModeNo}, false)
	h.start(t)

	got := h.waitForHealth(t, health.StatusUnhealthy)
	if got.FailingStreak < quickCheck.Retries || !strings.Contains(got.LastOutput, "refused") {
		t.Errorf("health = %+v, want the failures and what the probe said", got)
	}

	time.Sleep(50 * time.Millisecond)
	if status := h.status(t); status.State != StateRunning || h.starter.vmmCount() != 1 {
		t.Errorf("state = %s with %d VMMs launched, want the first still running", status.State, h.starter.vmmCount())
	}
}

// An on-failure:N instance that is still unhealthy after its N restarts
// ends as Failed, as one that keeps exiting with an error does.
func TestUnhealthyInstanceFailsOnceRestartsAreUsedUp(t *testing.T) {
	h, _ := monitored(t, RestartPolicy{Mode: RestartModeOnFailure, MaxRetries: 1}, false)
	h.restartAtOnce()
	h.start(t)

	status := h.waitForState(t, StateFailed)
	if !strings.Contains(status.StateError, "gave up after 1 restart") ||
		!strings.Contains(status.StateError, "health check") {
		t.Errorf("state error = %q, want the restarts given up after and the failing check", status.StateError)
	}
	if n := h.starter.vmmCount(); n != 2 {
		t.Errorf("launched %d VMMs, want 2: the first start and its one restart", n)
	}
	if _, _, ok := h.manager.Health(h.instance); ok {
		t.Error("the failed instance is still being probed")
	}
}

// Whatever ends the run ends its health checks: nothing is left probing a
// VM that is gone.
func TestStopEndsHealthChecks(t *testing.T) {
	h, probe := monitored(t, RestartPolicy{}, true)
	h.start(t)
	h.waitForHealth(t, health.StatusHealthy)

	if err := h.manager.Stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, _, ok := h.manager.Health(h.instance); ok {
		t.Error("a stopped instance is still monitored")
	}

	before := probe.count()
	time.Sleep(50 * time.Millisecond)
	if after := probe.count(); after != before {
		t.Errorf("%d probes after the stop, want none", after-before)
	}
}

// A daemon shutting down stops checking, and does not wait for the VMMs it
// checks, which outlive it: the next daemon checks them.
func TestCloseStopsHealthChecks(t *testing.T) {
	h, probe := monitored(t, RestartPolicy{}, true)
	h.start(t)
	h.waitForHealth(t, health.StatusHealthy)

	closed := make(chan struct{})
	go func() {
		h.manager.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close is still waiting on the health monitor")
	}

	before := probe.count()
	time.Sleep(50 * time.Millisecond)
	if after := probe.count(); after != before {
		t.Errorf("%d probes after Close, want none", after-before)
	}
}

// A paused guest cannot answer, and has not failed for it.
func TestPausedInstanceIsNotProbed(t *testing.T) {
	h, probe := monitored(t, RestartPolicy{Mode: RestartModeAlways}, false)
	h.start(t)
	if err := h.manager.Pause(t.Context(), h.instance); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	// A probe already on its way when the guest was paused finishes.
	time.Sleep(2 * quickCheck.Interval)

	before := probe.count()
	time.Sleep(50 * time.Millisecond)
	if after := probe.count(); after != before {
		t.Errorf("%d probes while paused, want none", after-before)
	}
	if status := h.status(t); status.State != StatePaused {
		t.Errorf("state = %s, want Paused", status.State)
	}
}

// A guest whose agent predates health checks cannot be checked, which is no
// evidence against it.
func TestOutdatedAgentIsNotUnhealthy(t *testing.T) {
	h, probe := monitored(t, RestartPolicy{Mode: RestartModeAlways}, false)
	probe.set(false, grpcstatus.Error(codes.Unimplemented, "unknown method Probe"))
	h.start(t)

	for probe.count() < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, got, _ := h.manager.Health(h.instance); got.Status != health.StatusStarting {
		t.Errorf("health = %s, want starting", got.Status)
	}
	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state = %s, want Running", status.State)
	}
}

// An instance with no check of its own gets its image's, unless it switches
// it off.
func TestHealthCheckComesFromTheImage(t *testing.T) {
	imageCheck := &health.Check{TCP: &health.TCPProbe{Port: 5432}}

	for name, tt := range map[string]struct {
		own       *health.Check
		monitored bool
	}{
		"inherited":    {own: nil, monitored: true},
		"switched off": {own: &health.Check{Disabled: true}, monitored: false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			images, ok := h.manager.images.(*fakeImages)
			if !ok {
				t.Fatalf("images is %T", h.manager.images)
			}
			images.held = &image.Image{
				Name: "img", Digest: "sha256:aaaa", DiskPath: images.diskPath,
				Entrypoint: []string{"/bin/sh"}, HealthCheck: imageCheck,
			}
			h.instance.HealthCheck = tt.own
			h.store.instances[h.instance.Name] = h.instance
			h.start(t)

			check, _, ok := h.manager.Health(h.instance)
			if ok != tt.monitored {
				t.Fatalf("monitored = %v, want %v", ok, tt.monitored)
			}
			if ok && (check.TCP == nil || check.Interval != health.DefaultInterval) {
				t.Errorf("check = %+v, want the image's, with the defaults", check)
			}
		})
	}
}
