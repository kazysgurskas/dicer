// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"errors"
	"fmt"
	"time"

	"github.com/nrednav/cuid2"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/event"
	"github.com/konradasb/dicer/internal/humanize"
)

// EnsureDefault puts the default kernel this binary carries on the host, and
// defines it once it is there. It extracts it again if its copy on the host
// is missing or damaged, and in place of the one an older version of Dicer
// carried.
func (m *Manager) EnsureDefault() error {
	want := Default()

	k, err := m.store.Kernel(DefaultName)
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
		now := time.Now()
		want.ID, want.CreatedAt, want.UpdatedAt = cuid2.Generate(), now, now
		if err := m.extractDefault(want.ID); err != nil {
			return err
		}
		if err := m.store.CreateKernel(want); err != nil {
			return fmt.Errorf("define the default kernel: %w", err)
		}
		m.record(want, event.ActionImported, fmt.Sprintf("Imported the default kernel for %s, version %s: %s",
			want.Architecture, DefaultVersion, humanize.Bytes(m.DiskBytes(want))))
		return nil
	case err != nil:
		return fmt.Errorf("define the default kernel: %w", err)
	case k.Architecture == want.Architecture && k.SHA256 == want.SHA256:
		if _, err := m.Path(k); err == nil {
			return nil
		}
		if err := m.extractDefault(k.ID); err != nil {
			return err
		}
		m.record(k, event.ActionImported,
			"Imported the default kernel again, as its copy on the host was missing or damaged")
		return nil
	}

	if err := m.extractDefault(k.ID); err != nil {
		return err
	}
	k.Architecture, k.SHA256, k.UpdatedAt = want.Architecture, want.SHA256, time.Now()
	if err := m.store.UpdateKernel(k); err != nil {
		return fmt.Errorf("update the default kernel: %w", err)
	}
	m.record(k, event.ActionUpdated, fmt.Sprintf("Updated the default kernel to version %s, which this version of Dicer carries",
		DefaultVersion))
	return nil
}

// extractDefault puts the default kernel this binary carries on the host, as
// the binary of the kernel with the given ID, in place of whatever is there.
func (m *Manager) extractDefault(id string) error {
	if err := m.removeBinary(id); err != nil {
		return fmt.Errorf("remove the previous default kernel: %w", err)
	}
	if _, err := Extract(m.binaryPath(id), DefaultVersion); err != nil {
		return fmt.Errorf("extract the default kernel: %w", err)
	}
	m.logger.Info("default kernel extracted", "version", DefaultVersion)
	return nil
}
