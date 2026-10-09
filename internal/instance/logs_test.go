// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/hypervisor"
)

// writeGuestLog puts a serial console log where the hypervisor would.
func writeGuestLog(t *testing.T, manager *Manager, instance Spec, contents string) string {
	t.Helper()

	path, err := manager.logPath(instance, LogSourceGuest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestStreamLogs(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")
	writeGuestLog(t, manager, instance, "booting\nready\n")

	var out bytes.Buffer
	if err := manager.StreamLogs(t.Context(), instance, LogOptions{}, &out); err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	if out.String() != "booting\nready\n" {
		t.Errorf("logs = %q, want the whole file", out.String())
	}
}

func TestStreamLogsTail(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")

	var sb strings.Builder
	for i := range 500 {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	writeGuestLog(t, manager, instance, sb.String())

	tests := []struct {
		tail      int
		wantLines int
		wantFirst string
	}{
		{tail: 3, wantLines: 3, wantFirst: "line 497"},
		{tail: 1, wantLines: 1, wantFirst: "line 499"},
		// Asking for more lines than there are gives the whole file.
		{tail: 5000, wantLines: 500, wantFirst: "line 0"},
	}

	for _, tt := range tests {
		var out bytes.Buffer
		if err := manager.StreamLogs(t.Context(), instance, LogOptions{TailLines: tt.tail}, &out); err != nil {
			t.Fatalf("StreamLogs(tail=%d): %v", tt.tail, err)
		}

		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != tt.wantLines {
			t.Errorf("tail=%d gave %d lines, want %d", tt.tail, len(lines), tt.wantLines)
		}
		if lines[0] != tt.wantFirst {
			t.Errorf("tail=%d starts at %q, want %q", tt.tail, lines[0], tt.wantFirst)
		}
	}
}

// TestStreamLogsTailSpansChunks covers a tail that has to read back through
// more than one chunk of the file.
func TestStreamLogsTailSpansChunks(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")

	line := strings.Repeat("x", 1000) + "\n"
	var sb strings.Builder
	for range 100 { // ~100 KiB, several chunks
		sb.WriteString(line)
	}
	sb.WriteString("last\n")
	writeGuestLog(t, manager, instance, sb.String())

	var out bytes.Buffer
	if err := manager.StreamLogs(t.Context(), instance, LogOptions{TailLines: 2}, &out); err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	if got := strings.Count(out.String(), "\n"); got != 2 {
		t.Errorf("got %d lines, want 2", got)
	}
	if !strings.HasSuffix(out.String(), "last\n") {
		t.Error("the tail does not end at the end of the file")
	}
}

// TestStreamLogsFollowStopsWithInstance checks that following a log ends
// when the instance does, rather than hanging on a file nothing will write
// to again.
func TestStreamLogsFollowStopsWithInstance(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")
	path := writeGuestLog(t, manager, instance, "booting\n")

	pid := os.Getpid()
	if err := manager.writeStatus(Status{InstanceID: instance.ID, State: StateRunning, VMMPID: &pid}); err != nil {
		t.Fatal(err)
	}

	var (
		mu   sync.Mutex
		out  bytes.Buffer
		done = make(chan error, 1)
	)
	go func() {
		done <- manager.StreamLogs(t.Context(), instance, LogOptions{Follow: true}, &lockedWriter{mu: &mu, w: &out})
	}()

	// Output written while it runs is followed.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Stopping the instance ends the stream.
	time.Sleep(2 * logPollInterval)
	forceState(t, manager, instance.ID, StateStopping)
	forceState(t, manager, instance.ID, StateStopped)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamLogs: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("following a log did not end when the instance stopped")
	}

	mu.Lock()
	defer mu.Unlock()
	if got := out.String(); !strings.Contains(got, "booting") || !strings.Contains(got, "ready") {
		t.Errorf("logs = %q, want what was written before and during the follow", got)
	}
}

// TestStreamLogsFollowsAStartingInstance checks that following the log of an
// instance still starting follows its boot, rather than ending before it has
// run: an attached 'dicer run' follows the console from the start.
func TestStreamLogsFollowsAStartingInstance(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")
	writeGuestLog(t, manager, instance, "booting\n")
	forceState(t, manager, instance.ID, StateStarting)

	done := make(chan error, 1)
	go func() {
		done <- manager.StreamLogs(t.Context(), instance, LogOptions{Follow: true}, &bytes.Buffer{})
	}()

	select {
	case err := <-done:
		t.Fatalf("following a starting instance's log ended at once: %v", err)
	case <-time.After(3 * logPollInterval):
	}

	forceState(t, manager, instance.ID, StateFailed)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("following a log did not end when the instance failed to start")
	}
}

// TestHypervisorLogIsKeptWithTheInstance checks that the hypervisor's log
// outlives its VMM, as the console log does, so that it can say why a
// launch failed or what a stopped guest was doing.
func TestHypervisorLogIsKeptWithTheInstance(t *testing.T) {
	tests := []struct {
		name string
		// end runs the instance and ends it, and returns what the
		// hypervisor wrote.
		end func(t *testing.T, h *harness) string
	}{
		{"failed start", func(t *testing.T, h *harness) string {
			h.starter.startErr = errors.New("kvm: permission denied")
			if err := h.manager.Start(t.Context(), h.instance); err == nil {
				t.Fatal("the start succeeded, want it to fail")
			}
			return "kvm: permission denied"
		}},
		{"failed resume from standby", func(t *testing.T, h *harness) string {
			h.start(t)
			if err := h.manager.Standby(t.Context(), h.instance); err != nil {
				t.Fatalf("Standby: %v", err)
			}
			h.starter.restoreErr = errors.New("kvm: permission denied")
			if err := h.manager.Start(t.Context(), h.instance); err == nil {
				t.Fatal("the resume succeeded, want it to fail")
			}
			return "kvm: permission denied"
		}},
		{"stop", func(t *testing.T, h *harness) string {
			h.start(t)
			writeHypervisorLog(t, h, "virtio-net: queue stalled\n")
			if err := h.manager.Stop(t.Context(), h.instance); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			return "virtio-net: queue stalled"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			want := tt.end(t, h)

			var out bytes.Buffer
			opts := LogOptions{Source: LogSourceHypervisor}
			if err := h.manager.StreamLogs(t.Context(), h.instance, opts, &out); err != nil {
				t.Fatalf("StreamLogs: %v", err)
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("hypervisor log = %q, want %q", out.String(), want)
			}
		})
	}
}

// writeHypervisorLog appends to the hypervisor's log where a running VMM
// writes it: through its link in the runtime directory.
func writeHypervisorLog(t *testing.T, h *harness, contents string) {
	t.Helper()

	path := hypervisor.LogPath(h.manager.hypervisorSocketPath(h.instance.ID))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(contents); err != nil {
		t.Fatal(err)
	}
}

func TestStreamLogsMissing(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")

	err := manager.StreamLogs(t.Context(), instance, LogOptions{}, &bytes.Buffer{})
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("StreamLogs with no log = %v, want ErrNotFound", err)
	}
}

func TestStreamLogsUnknownSource(t *testing.T) {
	manager, store, _ := newTestManager(t)
	instance := seedInstance(t, store, "web")

	err := manager.StreamLogs(t.Context(), instance, LogOptions{Source: "syslog"}, &bytes.Buffer{})
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("StreamLogs of an unknown source = %v, want ErrInvalidArgument", err)
	}
}

// lockedWriter lets the test read what has been written while the follow is
// still writing.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
