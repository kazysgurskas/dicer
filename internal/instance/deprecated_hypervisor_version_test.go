// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package instance

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/konradasb/dicer/internal/hypervisor"
)

// TestWarnDeprecatedHypervisorVersionsNamesWhatMustMove covers the warnings the
// daemon logs as it starts: one for each instance that names a deprecated
// version, each guest running on one and each memory snapshot taken with one,
// and none for what the version's removal leaves alone.
func TestWarnDeprecatedHypervisorVersionsNamesWhatMustMove(t *testing.T) {
	h := newHarness(t)
	// A newer default makes the harness's version deprecated.
	h.manager.starters[hypervisor.TypeCloudHypervisor] = []hypervisor.Starter{
		&fakeStarter{version: "v53.0.0"}, h.starter,
	}
	var logs bytes.Buffer
	h.manager.logger = slog.New(slog.NewTextHandler(&logs, nil))

	pinned := seedInstance(t, h.store, "pinned")
	pinned.HypervisorVersion = testHypervisorVersion
	h.store.instances[pinned.Name] = pinned
	seedInstance(t, h.store, "fresh")
	h.running(t)
	h.store.snapshots["old"] = Snapshot{
		Name: "old", Kind: SnapshotKindMemory,
		HypervisorType: hypervisor.TypeCloudHypervisor, HypervisorVersion: testHypervisorVersion,
	}
	h.store.snapshots["disk"] = Snapshot{
		Name: "disk", Kind: SnapshotKindDisk,
		HypervisorType: hypervisor.TypeCloudHypervisor, HypervisorVersion: testHypervisorVersion,
	}

	h.manager.WarnDeprecatedHypervisorVersions(t.Context())

	got := logs.String()
	for _, want := range []string{
		`msg="instance names a deprecated hypervisor version" instance=pinned`,
		`msg="instance's guest runs on a deprecated hypervisor version" instance=web`,
		`msg="memory snapshot was taken with a deprecated hypervisor version" snapshot=old`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("logs lack %s:\n%s", want, got)
		}
	}
	// An instance that names no version moves to the default as it boots,
	// and a disk snapshot needs no hypervisor.
	for _, unwanted := range []string{"instance=fresh", "snapshot=disk"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("logs warn about %s:\n%s", unwanted, got)
		}
	}
}

// TestBootWarnsOfDeprecatedHypervisorVersion covers the warning the daemon logs
// when it boots an instance on a deprecated version.
func TestBootWarnsOfDeprecatedHypervisorVersion(t *testing.T) {
	h := newHarness(t)
	h.manager.starters[hypervisor.TypeCloudHypervisor] = []hypervisor.Starter{
		&fakeStarter{version: "v53.0.0"}, h.starter,
	}
	var logs bytes.Buffer
	h.manager.logger = slog.New(slog.NewTextHandler(&logs, nil))

	h.instance.HypervisorVersion = testHypervisorVersion
	h.store.instances[h.instance.Name] = h.instance
	if err := h.manager.Start(t.Context(), h.instance); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if want := `msg="booting an instance on a deprecated hypervisor version" instance=web`; !strings.Contains(logs.String(), want) {
		t.Errorf("logs lack %s:\n%s", want, logs.String())
	}
}
