// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/konradasb/dicer/internal/hypervisor"
	"github.com/konradasb/dicer/internal/process"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// errFakeStarter is what the starter below returns for everything but its
// version, which is all these tests ask of it.
var errFakeStarter = errors.New("fake starter")

// fakeStarter is a hypervisor.Starter that only knows its version.
type fakeStarter struct{ version string }

func (f fakeStarter) Version() string           { return f.version }
func (f fakeStarter) DefaultKernelArgs() string { return "" }
func (f fakeStarter) PowerOffEndsVM() bool      { return true }

func (f fakeStarter) StartVM(
	context.Context, string, hypervisor.VMSpec,
) (*process.Process, hypervisor.Hypervisor, error) {
	return nil, nil, errFakeStarter
}

func (f fakeStarter) RestoreVM(
	context.Context, string, string, hypervisor.RestoreSpec,
) (*process.Process, hypervisor.Hypervisor, error) {
	return nil, nil, errFakeStarter
}

func (f fakeStarter) Connect(string) (hypervisor.Hypervisor, error) {
	return nil, errFakeStarter
}

func TestHypervisorInfosPutTheDefaultFirst(t *testing.T) {
	h := &hostHandler{starters: map[hypervisor.Type][]hypervisor.Starter{
		hypervisor.TypeFirecracker:     {fakeStarter{version: "v1.17.0"}},
		hypervisor.TypeCloudHypervisor: {fakeStarter{version: "v49.0.0"}, fakeStarter{version: "v48.0.0"}},
	}}

	got := h.hypervisorInfos()
	if len(got) != 2 {
		t.Fatalf("got %d hypervisors, want 2", len(got))
	}

	// The default hypervisor comes first whatever order the map iterates in.
	if got[0].GetType() != dicerdv1.HypervisorType_HYPERVISOR_TYPE_CLOUD_HYPERVISOR || !got[0].GetIsDefault() {
		t.Errorf("first entry = %+v, want cloud-hypervisor as the default", got[0])
	}
	if want := []string{"v49.0.0", "v48.0.0"}; len(got[0].GetVersions()) != 2 ||
		got[0].GetVersions()[0] != want[0] || got[0].GetVersions()[1] != want[1] {
		t.Errorf("versions = %v, want %v with the default first", got[0].GetVersions(), want)
	}
	if got[1].GetType() != dicerdv1.HypervisorType_HYPERVISOR_TYPE_FIRECRACKER || got[1].GetIsDefault() {
		t.Errorf("second entry = %+v, want firecracker, not the default", got[1])
	}
}

// TestHypervisorInfosDeprecateAllButTheDefault covers the deprecation
// policy: every version but a hypervisor's default is deprecated.
func TestHypervisorInfosDeprecateAllButTheDefault(t *testing.T) {
	h := &hostHandler{starters: map[hypervisor.Type][]hypervisor.Starter{
		hypervisor.TypeCloudHypervisor: {
			fakeStarter{version: "v53.0.0"}, fakeStarter{version: "v49.0.0"}, fakeStarter{version: "v48.0.0"},
		},
		hypervisor.TypeFirecracker: {fakeStarter{version: "v1.17.0"}},
	}}

	got := h.hypervisorInfos()
	if len(got) != 2 {
		t.Fatalf("got %d hypervisors, want 2", len(got))
	}
	if got, want := got[0].GetDeprecatedVersions(), []string{"v49.0.0", "v48.0.0"}; !slices.Equal(got, want) {
		t.Errorf("cloud-hypervisor's deprecated versions = %v, want %v", got, want)
	}
	if got := got[1].GetDeprecatedVersions(); len(got) != 0 {
		t.Errorf("firecracker's deprecated versions = %v, want none", got)
	}
}

// TestHypervisorInfosOmitMissingDrivers covers a daemon that could not build
// a starter: what it cannot start must not be advertised.
func TestHypervisorInfosOmitMissingDrivers(t *testing.T) {
	h := &hostHandler{starters: map[hypervisor.Type][]hypervisor.Starter{
		hypervisor.TypeCloudHypervisor: {fakeStarter{version: "v49.0.0"}},
	}}

	got := h.hypervisorInfos()
	if len(got) != 1 || got[0].GetType() != dicerdv1.HypervisorType_HYPERVISOR_TYPE_CLOUD_HYPERVISOR {
		t.Errorf("hypervisorInfos() = %+v, want only cloud-hypervisor", got)
	}
}

// TestListenerAddressesOfAListenerOnAllAddressesAreTheHosts checks that a
// client is told addresses it can connect to, not [::]:9000.
func TestListenerAddressesOfAListenerOnAllAddressesAreTheHosts(t *testing.T) {
	hostAddresses := func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.10.0.101"), netip.MustParseAddr("2001:db8::10")}, nil
	}
	failing := func() ([]netip.Addr, error) { return nil, errors.New("no interfaces") }

	tests := []struct {
		name          string
		listenAddress string
		hostAddresses func() ([]netip.Addr, error)
		want          []string
	}{
		{"not served over TCP", "", hostAddresses, nil},
		{"one address", "192.0.2.1:7443", hostAddresses, []string{"192.0.2.1:7443"}},
		{"all IPv4 addresses", "0.0.0.0:7443", hostAddresses, []string{"10.10.0.101:7443", "[2001:db8::10]:7443"}},
		{"all addresses", "[::]:9000", hostAddresses, []string{"10.10.0.101:9000", "[2001:db8::10]:9000"}},
		{"the host's unknown", "[::]:9000", nil, []string{"[::]:9000"}},
		{"the host's unreadable", "[::]:9000", failing, []string{"[::]:9000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &hostHandler{listenAddress: tt.listenAddress, hostAddresses: tt.hostAddresses}
			if got := h.listenerAddresses(); !slices.Equal(got, tt.want) {
				t.Errorf("listenerAddresses = %v, want %v", got, tt.want)
			}
		})
	}
}
