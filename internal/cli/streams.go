// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"io"

	"github.com/konradasb/dicer"
)

// streamLogs writes the log of the instance name that opts picks to w.
func streamLogs(ctx context.Context, client *dicer.Client, name string, opts dicer.LogOptions, w io.Writer) error {
	r, err := client.Instances.Logs(ctx, name, opts)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	_, err = io.Copy(w, r)
	return err
}
