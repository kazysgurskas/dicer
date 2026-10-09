// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package hypervisor

import "testing"

// TestOnlyListedTypesAreValid checks that every hypervisor type Types lists is
// valid, and one it does not list is not.
func TestOnlyListedTypesAreValid(t *testing.T) {
	for _, hypervisorType := range Types() {
		if !hypervisorType.Valid() {
			t.Errorf("%s is listed but not valid", hypervisorType)
		}
	}
	if Type("qemu").Valid() {
		t.Error("an unlisted type is valid")
	}
}
