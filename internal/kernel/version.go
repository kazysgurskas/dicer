// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

// Version identifies a release of Dicer's kernel,
// https://github.com/konradasb/dicer-kernel.
type Version string

// DefaultVersion is the release of Dicer's kernel this version of Dicer
// carries as the default kernel. A new release of Dicer may carry a newer
// one.
const DefaultVersion Version = "v6.18.53-1"

// Default returns the definition of the default kernel this binary carries,
// without an ID or times.
func Default() Kernel {
	return Kernel{Name: DefaultName, Architecture: defaultArchitecture, SHA256: defaultSHA256}
}
