// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package virtiofs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/konradasb/dicer/internal/atomicfile"
)

// Version is the virtiofsd dicerd embeds: the static build of
// github.com/konradasb/virtiofsd-static, which `make host-binaries`
// downloads into bin/$GOARCH/<Version>. Each architecture's file embeds only
// its own directory.
const Version = "v1.14.0-1"

// Extract atomically writes the embedded virtiofsd to dstPath, unless a file
// is already there, and returns dstPath.
func Extract(dstPath string) (string, error) {
	if _, err := os.Stat(dstPath); err == nil {
		return dstPath, nil
	}

	data, err := binaryFS.ReadFile(path.Join(binaryDir, Version, "virtiofsd"))
	if err != nil {
		return "", fmt.Errorf("read embedded virtiofsd: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
		return "", fmt.Errorf("create binaries dir: %w", err)
	}

	if err := atomicfile.Write(dstPath, data, 0o755); err != nil {
		return "", fmt.Errorf("write virtiofsd: %w", err)
	}

	return dstPath, nil
}
