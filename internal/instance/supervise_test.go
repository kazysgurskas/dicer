// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/konradasb/dicer/internal/errdefs"
)

func TestVMMCrashFailsInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	h.crash(t)
	status := h.waitForState(t, StateFailed)

	if !strings.Contains(status.StateError, "exited unexpectedly") ||
		!strings.Contains(status.StateError, "signal: killed") {
		t.Errorf("state error = %q, want the unexpected exit and its signal", status.StateError)
	}
	if status.VMMPID != nil || status.HypervisorSocketPath != "" {
		t.Errorf("status still names the dead process: pid=%v socket=%q",
			status.VMMPID, status.HypervisorSocketPath)
	}
	if h.manager.vmm(h.instance.ID) != nil {
		t.Error("the dead VMM is still registered")
	}

	// Host resources go with the VMM, not on the next request.
	if len(h.hostNetwork.removedTAPs) != 1 || h.hostNetwork.removedTAPs[0] != h.instance.ID {
		t.Errorf("removed TAPs = %v, want [%s]", h.hostNetwork.removedTAPs, h.instance.ID)
	}
	if len(h.hostNetwork.tornDownBridges) != 1 {
		t.Errorf("torn down bridges = %v, want the bridge to go with its last instance", h.hostNetwork.tornDownBridges)
	}

	// And a failed instance can simply be started again, even though the
	// dead VMM's sockets are still in the runtime directory it left behind:
	// a real hypervisor refuses to bind over them.
	stale := []string{
		filepath.Join(h.manager.runtimeDir(h.instance.ID), hypervisorSocketFile),
		filepath.Join(h.manager.runtimeDir(h.instance.ID), vsockSocketFile),
	}
	for _, path := range stale {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	h.start(t)
	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state after restart = %s, want Running", status.State)
	}
	for _, path := range stale {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stale socket %s survived the restart", filepath.Base(path))
		}
	}
}

func TestPausedVMMCrashFailsInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.Pause(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	h.crash(t)
	h.waitForState(t, StateFailed)
}

func TestStopIsNotACrash(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	vmm := h.starter.vmm()

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-vmm.Done():
	default:
		t.Error("Stop returned before the VMM exited")
	}

	// Give a watcher that wrongly took the exit for a crash time to act.
	time.Sleep(50 * time.Millisecond)
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s (%s), want Stopped", status.State, status.StateError)
	}
}

// TestStopKillsVMMThatIgnoresShutdown covers a VMM that does not exit when
// asked: Stop must not return while it is still running.
func TestStopKillsVMMThatIgnoresShutdown(t *testing.T) {
	h := newHarness(t)
	h.hv.onShutdown = nil
	h.manager.shutdownTimeout = 10 * time.Millisecond
	h.start(t)
	vmm := h.starter.vmm()

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-vmm.Done():
	default:
		t.Error("Stop returned while the VMM was still running")
	}
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s, want Stopped", status.State)
	}
}

func TestForcedDeleteIsNotACrash(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	vmm := h.starter.vmm()

	if err := h.manager.delete(t.Context(), h.instance, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	select {
	case <-vmm.Done():
	default:
		t.Error("Delete returned before the VMM exited")
	}

	time.Sleep(50 * time.Millisecond)
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s (%s), want no status left", status.State, status.StateError)
	}
}

// TestOldVMMExitDoesNotTouchNewOne guards the identity check: a watcher that
// fires late, for a VMM that was stopped and replaced, must leave the new one
// alone.
func TestOldVMMExitDoesNotTouchNewOne(t *testing.T) {
	h := newHarness(t)
	h.start(t)
	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.start(t)

	time.Sleep(50 * time.Millisecond)
	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state = %s (%s), want Running", status.State, status.StateError)
	}
	if h.manager.vmm(h.instance.ID) != h.starter.vmm() {
		t.Error("the new VMM is no longer registered")
	}
}

func TestRestoredVMMCrashFailsInstance(t *testing.T) {
	h := newHarness(t)
	h.running(t)
	snapshot, err := h.manager.createSnapshot(t.Context(), h.instance, "snap")
	if err != nil {
		t.Fatal(err)
	}
	h.stopped(t)
	if _, err := h.manager.restoreSnapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	h.crash(t)
	h.waitForState(t, StateFailed)
}

// TestCloseStopsWatching checks that the daemon shutting down leaves its VMMs
// and their recorded state alone.
func TestCloseStopsWatching(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	h.manager.Close()
	h.crash(t)
	<-h.starter.vmm().Done()

	time.Sleep(50 * time.Millisecond)
	if status := h.status(t); status.State != StateRunning {
		t.Errorf("state = %s, want Running: a closed manager watches nothing", status.State)
	}
}

// A workload that exits 0 is a clean end: the instance is Stopped, not
// Failed, and says how it ended.
func TestCleanExitStopsInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	h.exit(t, 0)
	status := h.waitForState(t, StateStopped)

	if status.ExitCode == nil || *status.ExitCode != 0 {
		t.Errorf("exit code = %v, want 0", status.ExitCode)
	}
	if status.StateError != "" || status.FinishedAt.IsZero() {
		t.Errorf("status = %+v, want no error and a finish time", status)
	}
	if len(h.hostNetwork.removedTAPs) != 1 {
		t.Errorf("removed TAPs = %v, want the instance's network released", h.hostNetwork.removedTAPs)
	}
}

func TestNonZeroExitFailsInstance(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	h.exit(t, 3)
	status := h.waitForState(t, StateFailed)

	if status.ExitCode == nil || *status.ExitCode != 3 {
		t.Errorf("exit code = %v, want 3", status.ExitCode)
	}
	if !strings.Contains(status.StateError, "exit code 3") {
		t.Errorf("state error = %q, want the exit code", status.StateError)
	}
}

func TestOnFailureRestartsCrashedInstance(t *testing.T) {
	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeOnFailure})
	h.restartAtOnce()
	h.start(t)

	h.crash(t)
	h.waitForVMMs(t, 2)
	status := h.waitForState(t, StateRunning)

	if status.RestartCount != 1 {
		t.Errorf("restart count = %d, want 1", status.RestartCount)
	}
	if h.manager.vmm(h.instance.ID) != h.starter.vmm() {
		t.Error("the restarted VMM is not supervised")
	}
	if got := testutil.ToFloat64(h.manager.metrics.restarts); got != 1 {
		t.Errorf("restarts recorded = %v, want 1", got)
	}
}

func TestOnFailureLeavesCleanExitStopped(t *testing.T) {
	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeOnFailure})
	h.restartAtOnce()
	h.start(t)

	h.exit(t, 0)
	h.waitForState(t, StateStopped)

	time.Sleep(50 * time.Millisecond)
	if n := h.starter.vmmCount(); n != 1 {
		t.Errorf("launched %d VMMs, want no restart after a clean exit", n)
	}
}

func TestAlwaysRestartsCleanExit(t *testing.T) {
	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeAlways})
	h.restartAtOnce()
	h.start(t)

	h.exit(t, 0)
	h.waitForVMMs(t, 2)
	h.waitForState(t, StateRunning)
}

func TestOnFailureGivesUpAfterMaxRetries(t *testing.T) {
	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeOnFailure, MaxRetries: 1})
	h.restartAtOnce()
	h.start(t)

	h.exit(t, 1)
	h.waitForVMMs(t, 2)
	h.waitForState(t, StateRunning)

	h.exit(t, 1)
	status := h.waitForState(t, StateFailed)

	if !strings.Contains(status.StateError, "gave up after 1 restart:") ||
		!strings.Contains(status.StateError, "exit code 1") {
		t.Errorf("state error = %q, want the give-up and its cause", status.StateError)
	}
}

// A restart that cannot start is an end like any other: it counts against
// the retry limit and backs off, rather than being retried in a hot loop or
// abandoned at the first try.
func TestFailedRestartIsRetried(t *testing.T) {
	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeOnFailure, MaxRetries: 2})
	h.restartAtOnce()
	h.start(t)

	h.starter.startErr = errors.New("no hypervisor today")
	h.crash(t)
	status := h.waitForState(t, StateFailed)

	if status.RestartCount != 2 {
		t.Errorf("restart count = %d, want both retries used", status.RestartCount)
	}
	if !strings.Contains(status.StateError, "gave up after 2 restarts") ||
		!strings.Contains(status.StateError, "no hypervisor today") {
		t.Errorf("state error = %q, want the give-up and why the restart failed", status.StateError)
	}
}

// restartingHarness returns a harness whose instance crashed and is waiting
// for a restart that is not due for a long time.
func restartingHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeAlways})
	h.manager.restartWait = func(time.Time) time.Duration { return time.Hour }
	h.start(t)

	h.crash(t)
	status := h.waitForState(t, StateRestarting)
	if status.NextRestartAt.IsZero() || status.StateError == "" {
		t.Fatalf("status = %+v, want when it restarts and why", status)
	}
	return h
}

func TestStopCancelsPendingRestart(t *testing.T) {
	h := restartingHarness(t)

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s, want Stopped", status.State)
	}
	if len(h.manager.restarts) != 0 {
		t.Error("the restart is still pending")
	}
	if instance, _ := h.store.Instance(h.instance.ID); !instance.StoppedByUser {
		t.Error("the stop is not recorded as a user's")
	}
}

// Starting a Restarting instance starts it now, and as a fresh start: its
// restarts in a row are forgotten.
func TestStartDuringRestartStartsNow(t *testing.T) {
	h := restartingHarness(t)

	h.start(t)

	status := h.status(t)
	if status.State != StateRunning || status.RestartCount != 0 {
		t.Errorf("status = %s with %d restarts, want Running with none", status.State, status.RestartCount)
	}
	if len(h.manager.restarts) != 0 {
		t.Error("the restart is still pending")
	}
}

func TestDeleteCancelsPendingRestart(t *testing.T) {
	h := restartingHarness(t)

	if err := h.manager.delete(t.Context(), h.instance, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(h.manager.restarts) != 0 {
		t.Error("the restart is still pending")
	}
}

// A daemon shutting down leaves a Restarting instance for the next one.
func TestCloseLeavesRestartForNextDaemon(t *testing.T) {
	h := restartingHarness(t)

	h.manager.Close()

	if status := h.status(t); status.State != StateRestarting {
		t.Errorf("state = %s, want Restarting", status.State)
	}
}

// gracefulHarness returns a harness whose guest answers a request to shut
// down by ending its VMM, as dicer-init does once the workload has stopped,
// if ends says so. It counts the requests, and the hypervisor shutdowns a
// stop falls back to.
func gracefulHarness(t *testing.T, ends bool) (h *harness, asked, forced *atomic.Int32) {
	t.Helper()

	h = newHarness(t)
	asked, forced = &atomic.Int32{}, &atomic.Int32{}

	h.manager.shutdownGuest = func(context.Context, string) error {
		asked.Add(1)
		if ends {
			_ = h.starter.vmm().Kill()
		}
		return nil
	}
	h.hv.onShutdown = func() {
		forced.Add(1)
		_ = h.starter.vmm().Kill()
	}
	return h, asked, forced
}

// A stop asks the guest to shut down, and waits for it, rather than ending
// the VM under a workload that may be writing.
func TestStopShutsTheGuestDownGracefully(t *testing.T) {
	h, asked, forced := gracefulHarness(t, true)
	h.start(t)

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if asked.Load() != 1 || forced.Load() != 0 {
		t.Errorf("asked %d times, forced %d times; want the guest asked once and nothing forced",
			asked.Load(), forced.Load())
	}
	if status := h.status(t); status.State != StateStopped {
		t.Errorf("state = %s, want Stopped", status.State)
	}
}

// A guest that does not shut down in its grace period is ended regardless.
func TestStopEndsAGuestThatIgnoresTheShutdown(t *testing.T) {
	h, asked, forced := gracefulHarness(t, false)
	h.manager.stopGracePeriod = 20 * time.Millisecond
	h.start(t)

	started := time.Now()
	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if asked.Load() != 1 || forced.Load() != 1 {
		t.Errorf("asked %d times, forced %d times; want both once", asked.Load(), forced.Load())
	}
	if waited := time.Since(started); waited < h.manager.stopGracePeriod {
		t.Errorf("Stop took %s, less than the grace period", waited)
	}
}

// A guest that cannot be asked -- its agent is too old, or unreachable -- is
// ended at once, not after waiting out a period it was never told about.
func TestStopEndsAGuestThatCannotBeAsked(t *testing.T) {
	h, _, forced := gracefulHarness(t, true)
	h.manager.shutdownGuest = func(context.Context, string) error { return errors.New("unimplemented") }
	h.manager.stopGracePeriod = time.Hour
	h.start(t)

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if forced.Load() != 1 {
		t.Errorf("forced %d times, want the VMM shut down directly", forced.Load())
	}
}

// A forced delete does not wait on the workload, as docker rm -f does not.
func TestForcedDeleteIsNotGraceful(t *testing.T) {
	h, asked, _ := gracefulHarness(t, true)
	h.start(t)

	if err := h.manager.delete(t.Context(), h.instance, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if asked.Load() != 0 {
		t.Errorf("the guest was asked to shut down %d times, want none", asked.Load())
	}
}

// A paused guest cannot answer: it is not asked.
func TestPausedGuestIsNotAskedToShutDown(t *testing.T) {
	h, asked, forced := gracefulHarness(t, true)
	h.manager.stopGracePeriod = time.Hour
	h.start(t)
	if err := h.manager.Pause(t.Context(), h.instance.Name); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if asked.Load() != 0 || forced.Load() != 1 {
		t.Errorf("asked %d times, forced %d times; want only forced", asked.Load(), forced.Load())
	}
}

// setRemoveOnExit has the harness instance ask to be deleted when it stops,
// in the definition the manager reads as well as the harness's copy.
func (h *harness) setRemoveOnExit(t *testing.T) {
	t.Helper()

	h.instance.RemoveOnExit = true
	h.store.instances[h.instance.Name] = h.instance
}

// waitForRemoval waits until the instance is gone from the store. The delete
// runs after the stop that triggered it, so it is not there the instant the
// instance stops.
func (h *harness) waitForRemoval(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := h.store.Instance(h.instance.ID); errors.Is(err, errdefs.ErrNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the instance was not deleted when it stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A guest that ends on its own takes the instance with it, which is what
// 'dicer run --rm' asks for.
func TestRemoveOnExitDeletesTheInstanceWhenItsGuestEnds(t *testing.T) {
	h := newHarness(t)
	h.setRemoveOnExit(t)
	h.start(t)

	h.exit(t, 0)
	h.waitForRemoval(t)
}

// A guest that failed is deleted too: --rm says what happens when it stops,
// not how it stopped.
func TestRemoveOnExitDeletesAFailedInstance(t *testing.T) {
	h := newHarness(t)
	h.setRemoveOnExit(t)
	h.start(t)

	h.exit(t, 1)
	h.waitForRemoval(t)
}

// Stopping it is stopping it, so it goes then too.
func TestRemoveOnExitDeletesAStoppedInstance(t *testing.T) {
	h := newHarness(t)
	h.setRemoveOnExit(t)
	h.start(t)

	if err := h.manager.stop(t.Context(), h.instance); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.waitForRemoval(t)
}

// An instance its restart policy will start again has not finished with the
// host, and is not deleted out from under the restart.
func TestRemoveOnExitKeepsAnInstanceThatWillRestart(t *testing.T) {
	h := newHarness(t)
	h.setRemoveOnExit(t)
	h.setRestart(t, RestartPolicy{Mode: RestartModeAlways})
	h.restartAtOnce()
	h.start(t)

	h.exit(t, 0)

	// It comes back rather than going away.
	h.waitForVMMs(t, 2)
	if _, err := h.store.Instance(h.instance.ID); err != nil {
		t.Fatalf("a restarting instance was deleted: %v", err)
	}
}

// Without it, an instance that stops is left where it was.
func TestAnInstanceThatDidNotAskIsNotDeleted(t *testing.T) {
	h := newHarness(t)
	h.start(t)

	h.exit(t, 0)
	h.waitForState(t, StateStopped)

	if _, err := h.store.Instance(h.instance.ID); err != nil {
		t.Errorf("an instance that did not ask to be deleted was: %v", err)
	}
}
