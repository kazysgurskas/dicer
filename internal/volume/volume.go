// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package volume

import (
	"time"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
)

// Volume is persistent storage an instance can mount, outliving the
// instances that use it.
type Volume struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	Path      string    `yaml:"path"`
	SizeBytes int64     `yaml:"size_bytes"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// Validate returns an invalid argument error unless the volume has a valid
// name and a size.
func (v Volume) Validate() error {
	if err := naming.Validate(v.Name); err != nil {
		return err
	}
	if v.SizeBytes <= 0 {
		return errdefs.InvalidArgument("size_bytes must be greater than 0")
	}
	return nil
}
