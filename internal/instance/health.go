// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/konradasb/dicer/internal/events"
	"github.com/konradasb/dicer/internal/health"
	"github.com/konradasb/dicer/internal/process"
	diceragentv1 "github.com/konradasb/dicer/proto/diceragent/v1"
)

// Health monitors are started by supervise and stopped by forget. Probes run
// in the guest agent over vsock. If an instance's restart policy restarts
// failures, an unhealthy instance is stopped as failed, and the policy then
// restarts it or gives up. Under the policy no, it is only reported.

// Health returns an instance's check and its findings, or false if it is not
// being monitored.
func (m *Manager) Health(instance Spec) (health.Check, health.Health, bool) {
	s := m.supervision(instance.ID)
	if s == nil || s.health == nil {
		return health.Check{}, health.Health{}, false
	}
	return s.health.Check(), s.health.Health(), true
}

// monitor probes an instance's health until ctx is done or the Manager
// closes. Each probe starts an interval after the previous one finished.
func (m *Manager) monitor(ctx context.Context, instance Spec, vmm *process.Process, vsockPath string, h *health.Monitor) {
	timer := time.NewTimer(h.Check().Interval)
	defer timer.Stop()

	warnedOutdated := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closing:
			return
		case <-timer.C:
		}

		if m.probeOnce(ctx, instance, vmm, vsockPath, h) && !warnedOutdated {
			m.logger.WarnContext(ctx, "the guest agent cannot run health checks until the instance restarts",
				"instance", instance.Name)
			warnedOutdated = true
		}
		timer.Reset(h.Check().Interval)
	}
}

// probeOnce runs one probe, records the result and acts on unhealthy. It
// reports whether the guest's agent is too old to probe, which is not a
// failure.
func (m *Manager) probeOnce(ctx context.Context, instance Spec, vmm *process.Process, vsockPath string, h *health.Monitor) bool {
	// A paused guest cannot answer, and has not failed for it.
	if status, err := m.Status(instance); err != nil || status.State != StateRunning {
		return false
	}

	check := h.Check()
	result, err := m.probe(ctx, vsockPath, check)
	switch {
	case ctx.Err() != nil:
		return false
	case grpcstatus.Code(err) == codes.Unimplemented:
		return true
	}

	before, after := h.Observe(result)
	if after.Status != before.Status {
		m.logger.InfoContext(ctx, "instance health changed", "instance", instance.Name,
			"health", after.Status, "check", check.String(), "output", firstLine(after.LastOutput))
		m.recordHealth(instance, check, after)
	}
	// Checked on every probe: the restart policy may have changed.
	if after.Status == health.StatusUnhealthy {
		m.handleUnhealthy(ctx, instance, vmm, check, after)
	}
	return false
}

// probeGuest runs one probe in the guest behind vsockPath.
func probeGuest(ctx context.Context, vsockPath string, check health.Check) (health.Result, error) {
	conn, err := dialAgent(vsockPath)
	if err != nil {
		return health.Result{Output: err.Error(), At: time.Now()}, err
	}
	defer func() { _ = conn.Close() }()

	return health.Probe(ctx, diceragentv1.NewAgentServiceClient(conn), check)
}

// handleUnhealthy stops an unhealthy instance and ends it as failed, if its
// restart policy restarts failures. The policy then restarts it, or leaves
// it Failed once it has used up its restarts.
func (m *Manager) handleUnhealthy(ctx context.Context, instance Spec, vmm *process.Process, check health.Check, verdict health.Health) {
	lock := m.lock(instance.ID)
	lock.Lock()
	defer lock.Unlock()

	if m.vmm(instance.ID) != vmm {
		// Stopped, deleted or replaced while we waited for the lock.
		return
	}
	if current, err := m.store.Instance(instance.ID); err == nil {
		instance = current
	}
	status, err := m.Status(instance)
	if err != nil || status.State != StateRunning {
		return
	}

	exit := Exit{Failure: fmt.Errorf("health check %q failed %d times in a row: %s",
		check.String(), verdict.FailingStreak, firstLine(verdict.LastOutput))}
	// An instance whose policy gave up is stopped too. Left running, it
	// would be restarted once it had run long enough to reset the restart
	// count, and the limit would never end it.
	decision := decideRestart(instance.Restart, exit, status.RestartCount, time.Since(status.StartedAt))
	if !decision.restart && !decision.gaveUp {
		return
	}

	m.logger.WarnContext(ctx, "instance is unhealthy, stopping it for its restart policy",
		"instance", instance.Name, "restart_policy", instance.Restart.String())

	// Stopping the VMM cancels this monitor's ctx.
	ctx = context.WithoutCancel(ctx)
	m.stopVMM(ctx, instance, status, true)
	m.ended(ctx, instance, status, exit)
}

// recordHealth records a healthy or unhealthy verdict.
func (m *Manager) recordHealth(instance Spec, check health.Check, verdict health.Health) {
	switch verdict.Status {
	case health.StatusHealthy:
		m.record(instance, events.ActionHealthy,
			fmt.Sprintf("Health check %q passed: %s", check.String(), firstLine(verdict.LastOutput)), nil)
	case health.StatusUnhealthy:
		m.record(instance, events.ActionUnhealthy,
			fmt.Sprintf("Health check %q failed %d times in a row: %s",
				check.String(), verdict.FailingStreak, firstLine(verdict.LastOutput)),
			map[string]string{"failing_streak": strconv.Itoa(verdict.FailingStreak)})
	case health.StatusStarting:
	}
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
