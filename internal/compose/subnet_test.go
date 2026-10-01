// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package compose

import "testing"

func TestFreeSubnetIsTheFirstNoNetworkOverlaps(t *testing.T) {
	tests := []struct {
		name  string
		taken []string
		want  string
	}{
		{"none taken", nil, "10.213.0.0/24"},
		{"first taken", []string{"172.20.0.0/16", "10.213.0.0/24"}, "10.213.1.0/24"},
		{"a larger network takes several", []string{"10.213.0.0/23"}, "10.213.2.0/24"},
		{"a smaller one takes the one it is in", []string{"10.213.0.128/25"}, "10.213.1.0/24"},
		{"the whole pool", []string{"10.0.0.0/8"}, ""},
		{"not a subnet is ignored", []string{"not a subnet", "10.213.0.0/24"}, "10.213.1.0/24"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FreeSubnet(tt.taken)
			if tt.want == "" {
				if err == nil {
					t.Errorf("FreeSubnet(%q) = %s, want an error", tt.taken, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("FreeSubnet(%q) = %s, %v; want %s", tt.taken, got, err, tt.want)
			}
		})
	}
}
