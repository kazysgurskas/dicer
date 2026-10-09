// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hypervisor

import "slices"

// Type is the virtual machine monitor an instance runs on.
type Type string

const (
	// TypeCloudHypervisor is Cloud Hypervisor, the default.
	TypeCloudHypervisor Type = "cloud-hypervisor"

	// TypeFirecracker is Firecracker.
	TypeFirecracker Type = "firecracker"

	// DefaultType is what an instance that names none runs on.
	DefaultType = TypeCloudHypervisor
)

// Types returns every hypervisor an instance may run on, the default first.
func Types() []Type {
	return []Type{TypeCloudHypervisor, TypeFirecracker}
}

// Valid reports whether t names a hypervisor Dicer can start instances with.
func (t Type) Valid() bool {
	return slices.Contains(Types(), t)
}
