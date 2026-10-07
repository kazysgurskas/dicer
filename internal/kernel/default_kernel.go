// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package kernel

import (
	"fmt"
	"runtime"

	"github.com/konradasb/dicer/internal/types"
)

// defaultRelease is where the release of Dicer's kernel that this version of
// Dicer pins as the default kernel is downloaded from.
const defaultRelease = "https://github.com/konradasb/dicer-kernel/releases/download/v6.18.53-1/"

// defaultBinaries are the default kernel's binaries, by Go architecture.
var defaultBinaries = map[string]types.Kernel{
	"amd64": {
		Architecture: types.ArchitectureX86_64,
		URL:          defaultRelease + "vmlinux-x86_64",
		SHA256:       "ca5db6c291deb8a409db1f1ab14cc55ef6d35504daf17fc0f5577ffc1662b669",
	},
	"arm64": {
		Architecture: types.ArchitectureAArch64,
		URL:          defaultRelease + "Image-arm64",
		SHA256:       "1ce335854bc05535584dd57638f10832db91c4a20cbb76bab7851890c3d14568",
	},
}

// Default returns the definition of the default kernel for the host's
// architecture, without an ID or times, or an error on an architecture there
// is none for. A new release of Dicer may pin a newer one.
func Default() (types.Kernel, error) {
	k, ok := defaultBinaries[runtime.GOARCH]
	if !ok {
		return types.Kernel{}, fmt.Errorf("no default kernel for architecture %q", runtime.GOARCH)
	}

	k.Name = types.DefaultKernelName
	return k, nil
}
