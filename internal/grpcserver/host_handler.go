// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import (
	"context"
	"net/netip"
	"os"

	"github.com/konradasb/dicer/internal/hypervisor"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// hostHandler reports the daemon's version, hypervisors and addresses.
type hostHandler struct {
	version       string
	starters      map[hypervisor.Type][]hypervisor.Starter
	listenAddress string
	hostAddresses func() ([]netip.Addr, error)
	fingerprint   string
}

// GetHostInfo reports the daemon's version, hostname, hypervisors and API
// addresses, and the token the call was made with.
func (h *hostHandler) GetHostInfo(
	ctx context.Context, _ *dicerdv1.GetHostInfoRequest,
) (*dicerdv1.GetHostInfoResponse, error) {
	hostname, _ := os.Hostname()
	// A call over the socket has no token, and so no token's name.
	t, _ := tokenFrom(ctx)

	return &dicerdv1.GetHostInfoResponse{
		Version:           h.version,
		Hostname:          hostname,
		Hypervisors:       h.hypervisorInfos(),
		ListenerAddresses: h.listenerAddresses(),
		Fingerprint:       h.fingerprint,
		Token:             t.Name,
	}, nil
}

// hypervisorInfos describes the available hypervisors, the default first.
func (h *hostHandler) hypervisorInfos() []*dicerdv1.HypervisorInfo {
	out := make([]*dicerdv1.HypervisorInfo, 0, len(h.starters))

	for i, hypervisorType := range hypervisor.Types() {
		starters, ok := h.starters[hypervisorType]
		if !ok {
			continue
		}

		versions := make([]string, 0, len(starters))
		for _, s := range starters {
			versions = append(versions, s.Version())
		}

		out = append(out, &dicerdv1.HypervisorInfo{
			Type:               hypervisorTypes.toProto(hypervisorType),
			Versions:           versions,
			IsDefault:          i == 0,
			DeprecatedVersions: hypervisor.DeprecatedVersions(starters),
		})
	}

	return out
}

// listenerAddresses returns the addresses of the TCP listener: the one it
// is bound to, or, for a listener on all of the host's addresses, those
// addresses with its port. It returns none if the API is not served over
// TCP.
func (h *hostHandler) listenerAddresses() []string {
	if h.listenAddress == "" {
		return nil
	}

	listener, err := netip.ParseAddrPort(h.listenAddress)
	if err != nil || !listener.Addr().IsUnspecified() || h.hostAddresses == nil {
		return []string{h.listenAddress}
	}
	addrs, err := h.hostAddresses()
	if err != nil || len(addrs) == 0 {
		return []string{h.listenAddress}
	}

	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, netip.AddrPortFrom(a, listener.Port()).String())
	}
	return out
}
