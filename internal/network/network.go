// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

// Package network defines host-local networks, assigns their guests'
// addresses and names their interfaces. It is portable; internal/hostnet
// configures the host.
package network

import (
	"cmp"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/nrednav/cuid2"

	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/naming"
)

// DefaultName is the name of the default network, which the daemon creates
// itself and an instance joins when it names no network.
const DefaultName = "default"

// Network is a bridged network instances attach to, with an address pool of
// its own.
type Network struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Gateway     string   `yaml:"gateway"`
	Subnet      string   `yaml:"subnet"`
	Bridge      string   `yaml:"bridge"`
	Nameservers []string `yaml:"nameservers,omitempty"`
	MTU         int      `yaml:"mtu,omitempty"`
	Isolated    bool     `yaml:"isolated,omitempty"`
	// Internal stops the network's instances reaching anything beyond it:
	// the outside, other networks, the host's services and upstream
	// nameservers.
	Internal  bool      `yaml:"internal,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`

	// TotalIPs and FreeIPs count the addresses for instances. They are
	// computed when the network is read and not stored.
	TotalIPs int64 `yaml:"-"`
	FreeIPs  int64 `yaml:"-"`
}

// Allocation is one instance's address on a network, held for as long as the
// instance is defined.
type Allocation struct {
	NetworkID  string `yaml:"network_id"`
	InstanceID string `yaml:"instance_id"`
	IP         string `yaml:"ip"`
	MAC        string `yaml:"mac"`

	// InstanceName and TAPDevice are derived when the allocation is read
	// and not stored.
	InstanceName string `yaml:"-"`
	TAPDevice    string `yaml:"-"`
}

// The MTU range a network may have.
const (
	MinMTU = 576
	MaxMTU = 9000
)

// Validate returns an invalid argument error unless the network has a valid
// name, an MTU in range and nameservers that are IP addresses, and its
// settings agree with each other.
func (n Network) Validate() error {
	if err := naming.Validate(n.Name); err != nil {
		return err
	}
	if n.MTU < MinMTU || n.MTU > MaxMTU {
		return errdefs.InvalidArgument("MTU %d is out of range: want %d to %d", n.MTU, MinMTU, MaxMTU)
	}
	for _, nameserver := range n.Nameservers {
		if net.ParseIP(nameserver) == nil {
			return errdefs.InvalidArgument("nameserver %q is not an IP address", nameserver)
		}
	}
	if n.Internal && len(n.Nameservers) > 0 {
		return errdefs.InvalidArgument("an internal network cannot use nameservers, " +
			"since its instances cannot reach them: leave the nameservers out")
	}
	return nil
}

// Netmask returns the network's subnet mask in dotted-quad form.
func (n Network) Netmask() (string, error) {
	subnet, err := n.subnet()
	if err != nil {
		return "", err
	}

	return net.IP(subnet.Mask).String(), nil
}

// PrefixLen returns the length of the network's subnet prefix.
func (n Network) PrefixLen() (int, error) {
	subnet, err := n.subnet()
	if err != nil {
		return 0, err
	}

	ones, _ := subnet.Mask.Size()

	return ones, nil
}

// IPCounts returns how many addresses the network has for instances and how
// many of them are free with allocated taken. The network, broadcast and
// gateway addresses are excluded. Both are zero if the subnet is invalid.
func (n Network) IPCounts(allocated int) (total, free int64) {
	subnet, err := n.subnet()
	if err != nil {
		return 0, 0
	}

	ones, bits := subnet.Mask.Size()
	if bits == 0 {
		return 0, 0
	}

	total = max((int64(1)<<(bits-ones))-3, 0)
	free = max(total-int64(allocated), 0)

	return total, free
}

func (n Network) subnet() (*net.IPNet, error) {
	_, subnet, err := net.ParseCIDR(n.Subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet CIDR in network %q: %w", n.ID, err)
	}

	return subnet, nil
}

// Defaults applied to a network that does not specify them.
const (
	// DefaultNameserver is the nameserver a network forwards to.
	DefaultNameserver = "8.8.8.8"
	// DefaultMTU is a network's MTU.
	DefaultMTU = 1500
)

// Bandwidth limits the bytes per second an instance's guest sends and
// receives. Zero is unlimited.
type Bandwidth struct {
	UploadBytesPerSecond   int64
	DownloadBytesPerSecond int64
}

// maxPrefixLen is the longest prefix a network may have: a /30 is the
// smallest subnet with an address left for an instance once the network,
// gateway and broadcast addresses are set aside.
const maxPrefixLen = 30

// ParseSubnet parses a network's subnet: an IPv4 CIDR with room for at least
// one instance. The result is normalised, so "10.0.0.5/24" is 10.0.0.0/24.
func ParseSubnet(s string) (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, errdefs.InvalidArgument("invalid subnet %q: want an IPv4 CIDR such as 172.20.0.0/16", s)
	}
	if ipNet.IP.To4() == nil {
		return nil, errdefs.InvalidArgument("subnet %s is not IPv4; only IPv4 networks are supported", ipNet)
	}
	if ones, _ := ipNet.Mask.Size(); ones > maxPrefixLen {
		return nil, errdefs.InvalidArgument("subnet %s is too small: the longest prefix a network may have is /%d",
			ipNet, maxPrefixLen)
	}
	return ipNet, nil
}

// Assignable reports whether ip can be given out on ipNet: an address in it
// other than its network and broadcast addresses.
func Assignable(ipNet *net.IPNet, ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil || !ipNet.Contains(ip4) {
		return false
	}
	networkIP := ipNet.IP.To4()
	broadcastIP := make(net.IP, len(networkIP))
	for i := range networkIP {
		broadcastIP[i] = networkIP[i] | ^ipNet.Mask[i]
	}
	return !ip4.Equal(networkIP) && !ip4.Equal(broadcastIP)
}

// Spec is what a new network is asked to be. An unset gateway, MTU or
// nameservers take their defaults.
type Spec struct {
	Name        string
	Subnet      string
	Gateway     string
	MTU         int
	Nameservers []string
	Isolated    bool
	Internal    bool
}

// New returns the network spec asks for, with a new ID, or an invalid
// argument error. It does not check the subnet against other networks or the
// host's: see CheckSubnetOverlap.
func New(spec Spec) (Network, error) {
	if spec.Subnet == "" {
		return Network{}, errdefs.InvalidArgument("subnet is required")
	}
	subnet, err := ParseSubnet(spec.Subnet)
	if err != nil {
		return Network{}, err
	}
	gateway, err := gatewayOf(subnet, spec.Gateway)
	if err != nil {
		return Network{}, err
	}

	// An internal network gets no nameservers, since its instances cannot
	// reach them.
	nameservers := spec.Nameservers
	if len(nameservers) == 0 && !spec.Internal {
		nameservers = []string{DefaultNameserver}
	}

	now := time.Now()
	n := Network{
		ID:          cuid2.Generate(),
		Name:        spec.Name,
		Subnet:      subnet.String(),
		Gateway:     gateway,
		Bridge:      BridgeName(spec.Name),
		MTU:         cmp.Or(spec.MTU, DefaultMTU),
		Nameservers: nameservers,
		Isolated:    spec.Isolated,
		Internal:    spec.Internal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := n.Validate(); err != nil {
		return Network{}, err
	}
	return n, nil
}

// gatewayOf returns the requested gateway, or the subnet's first address.
func gatewayOf(subnet *net.IPNet, want string) (string, error) {
	if want == "" {
		first := slices.Clone(subnet.IP.To4())
		first[len(first)-1]++
		return first.String(), nil
	}

	gateway := net.ParseIP(want)
	if gateway == nil || !Assignable(subnet, gateway) {
		return "", errdefs.InvalidArgument("gateway %q is not an assignable address in subnet %s", want, subnet)
	}
	return gateway.To4().String(), nil
}

// CheckSubnetOverlap returns an ErrExists error if n's subnet overlaps one of
// networks, or one of hostSubnets, whose addresses n's would hide.
func CheckSubnetOverlap(n Network, networks []Network, hostSubnets []netip.Prefix) error {
	want, err := netip.ParsePrefix(n.Subnet)
	if err != nil {
		return errdefs.InvalidArgument("invalid subnet %q", n.Subnet)
	}

	for _, existing := range networks {
		have, err := netip.ParsePrefix(existing.Subnet)
		if err != nil {
			continue
		}
		if have.Overlaps(want) {
			return errdefs.Exists("subnet %s overlaps network %q (%s)", want, existing.Name, existing.Subnet)
		}
	}
	for _, p := range hostSubnets {
		if p.Overlaps(want) {
			return errdefs.Exists("subnet %s overlaps %s, which the host is on", want, p)
		}
	}
	return nil
}
