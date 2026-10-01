// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package network

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/dicer/internal/atomicfile"
	"github.com/konradasb/dicer/internal/errdefs"
	"github.com/konradasb/dicer/internal/types"
)

// allocationTableExt ends the name of a network's allocation table file.
const allocationTableExt = ".yaml"

// Manager assigns addresses from a subnet and persists the allocations, so an
// instance keeps its address across restarts. Allocations are released on
// delete and reconciled at daemon start.
//
// The allocation tables are read from disk once, when the Manager is made,
// and kept in memory, so that looking an address up -- as the networks' DNS
// servers do for every query -- reads no file. The Manager must be the only
// writer of its directory. It is safe for concurrent use.
type Manager struct {
	dir    string
	logger *slog.Logger

	// mu guards the allocation tables. Assignment is a read-modify-write
	// over a whole network, so one lock across all of them is both correct
	// and, at the scale of one host, cheap; lookups share it.
	mu               sync.RWMutex
	allocationTables map[string]*allocationTable
	// allocationTableErrors holds the error reading each allocation table
	// that could not be read: its network's allocations are unknown, so
	// every call about it returns that error, until Forget discards the
	// allocation table.
	allocationTableErrors map[string]error
}

// allocationTable is a network's allocations, indexed by instance and by
// address so that a lookup scans none of them.
type allocationTable struct {
	allocations []types.NetworkAllocation
	// byInstance and byIP are the index into allocations of each instance's
	// allocation, and of each address's.
	byInstance map[string]int
	byIP       map[string]int
}

// emptyAllocationTable is the allocation table of a network with no
// allocations. It is never changed: save makes a new allocation table.
var emptyAllocationTable = newAllocationTable(nil)

func newAllocationTable(allocations []types.NetworkAllocation) *allocationTable {
	t := &allocationTable{
		allocations: allocations,
		byInstance:  make(map[string]int, len(allocations)),
		byIP:        make(map[string]int, len(allocations)),
	}
	for i, allocation := range allocations {
		t.byInstance[allocation.InstanceID] = i
		t.byIP[allocation.IP] = i
	}
	return t
}

// Config configures a Manager.
type Config struct {
	// Dir is the directory the allocation tables are kept in.
	Dir string

	Logger *slog.Logger
}

// NewManager returns a Manager storing its allocation tables under cfg.Dir,
// with those already there read. One that cannot be read fails the calls
// about its network, not NewManager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", cfg.Dir, err)
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfg.Dir, err)
	}

	m := &Manager{
		dir:                   cfg.Dir,
		logger:                cfg.Logger.With("component", "network"),
		allocationTables:      make(map[string]*allocationTable),
		allocationTableErrors: make(map[string]error),
	}
	for _, e := range entries {
		// Skip atomicfile's temporary files, which start with a dot.
		network, ok := strings.CutSuffix(e.Name(), allocationTableExt)
		if !ok || e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		allocations, err := readAllocations(m.path(network))
		if err != nil {
			m.allocationTableErrors[network] = fmt.Errorf("allocations for %q: %w", network, err)
			continue
		}
		m.allocationTables[network] = newAllocationTable(allocations)
	}
	return m, nil
}

func (m *Manager) path(network string) string {
	return filepath.Join(m.dir, network+allocationTableExt)
}

// readAllocations reads an allocation table file.
func readAllocations(path string) ([]types.NetworkAllocation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var allocations []types.NetworkAllocation
	if err := yaml.Unmarshal(data, &allocations); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return allocations, nil
}

// allocationTableOf returns a network's allocation table,
// emptyAllocationTable if it has none. Must be called with the lock held.
func (m *Manager) allocationTableOf(network string) (*allocationTable, error) {
	if err := m.allocationTableErrors[network]; err != nil {
		return nil, err
	}
	if t, ok := m.allocationTables[network]; ok {
		return t, nil
	}
	return emptyAllocationTable, nil
}

// save writes a network's allocation table to its file and, once it is
// there, makes it the allocation table in memory: a failed write changes
// neither. Must be called with the lock held for writing.
func (m *Manager) save(network string, allocations []types.NetworkAllocation) error {
	data, err := yaml.Marshal(allocations)
	if err != nil {
		return fmt.Errorf("marshal allocations for %q: %w", network, err)
	}

	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", m.dir, err)
	}
	if err := atomicfile.Write(m.path(network), data, 0o600); err != nil {
		return err
	}

	m.allocationTables[network] = newAllocationTable(allocations)
	return nil
}

// List returns every allocation on a network. The slice is the caller's.
func (m *Manager) List(network string) ([]types.NetworkAllocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.allocationTableOf(network)
	if err != nil {
		return nil, err
	}
	return slices.Clone(t.allocations), nil
}

// Get returns the allocation held by an instance on a network.
func (m *Manager) Get(network, instanceID string) (types.NetworkAllocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.allocationTableOf(network)
	if err != nil {
		return types.NetworkAllocation{}, err
	}
	i, ok := t.byInstance[instanceID]
	if !ok {
		return types.NetworkAllocation{}, errdefs.NotFound(
			"instance %q has no address on network %q", instanceID, network)
	}
	return t.allocations[i], nil
}

// InstanceAt returns the ID of the instance holding ip on a network. It
// reports false if none does, or if the network's allocation table cannot
// be read.
func (m *Manager) InstanceAt(network, ip string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.allocationTableOf(network)
	if err != nil {
		return "", false
	}
	i, ok := t.byIP[ip]
	if !ok {
		return "", false
	}
	return t.allocations[i].InstanceID, true
}

// Allocate assigns an address to an instance, or returns the one it holds. A
// staticIP must be in the subnet and free.
func (m *Manager) Allocate(n types.Network, instanceID, staticIP string) (types.NetworkAllocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.allocationTableOf(n.Name)
	if err != nil {
		return types.NetworkAllocation{}, err
	}
	if i, ok := t.byInstance[instanceID]; ok {
		return t.allocations[i], nil
	}

	_, ipNet, err := net.ParseCIDR(n.Subnet)
	if err != nil {
		return types.NetworkAllocation{}, fmt.Errorf("invalid subnet %q: %w", n.Subnet, err)
	}

	used := make(map[string]struct{}, len(t.allocations)+1)
	used[n.Gateway] = struct{}{}
	for ip := range t.byIP {
		used[ip] = struct{}{}
	}

	var ip string
	if staticIP != "" {
		parsed := net.ParseIP(staticIP)
		if parsed == nil || !Assignable(ipNet, parsed) {
			return types.NetworkAllocation{}, errdefs.InvalidArgument(
				"static IP %q is not an assignable address in subnet %s", staticIP, n.Subnet)
		}
		ip = parsed.To4().String()
		if _, taken := used[ip]; taken {
			return types.NetworkAllocation{}, errdefs.InvalidState(
				"static IP %s is already in use on network %q", ip, n.Name)
		}
	} else {
		ip, err = allocateIP(ipNet, used)
		if err != nil {
			return types.NetworkAllocation{}, fmt.Errorf(
				"allocate address on network %q: %w", n.Name, err)
		}
	}

	mac, err := randomMAC()
	if err != nil {
		return types.NetworkAllocation{}, fmt.Errorf("generate MAC: %w", err)
	}

	allocation := types.NetworkAllocation{
		NetworkID:  n.ID,
		InstanceID: instanceID,
		IP:         ip,
		MAC:        mac,
	}

	if err := m.save(n.Name, append(slices.Clone(t.allocations), allocation)); err != nil {
		return types.NetworkAllocation{}, err
	}

	return allocation, nil
}

// Release drops an instance's allocation. Releasing one that does not exist
// is not an error, so cleanup paths can call it unconditionally.
func (m *Manager) Release(network, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.allocationTableOf(network)
	if err != nil {
		return err
	}
	i, ok := t.byInstance[instanceID]
	if !ok {
		return nil
	}

	return m.save(network, slices.Delete(slices.Clone(t.allocations), i, i+1))
}

// Forget discards a network's whole allocation table, for use when the
// network itself is deleted.
func (m *Manager) Forget(network string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.Remove(m.path(network)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove allocations for %q: %w", network, err)
	}
	delete(m.allocationTables, network)
	delete(m.allocationTableErrors, network)

	return nil
}

// Reconcile drops allocations on the given networks held by instances not in
// live, and returns how many it released.
func (m *Manager) Reconcile(networks []string, live map[string]struct{}) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var released int
	for _, network := range networks {
		t, err := m.allocationTableOf(network)
		if err != nil {
			return released, err
		}

		kept := make([]types.NetworkAllocation, 0, len(t.allocations))
		for _, allocation := range t.allocations {
			if _, ok := live[allocation.InstanceID]; ok {
				kept = append(kept, allocation)
				continue
			}
			released++
			m.logger.Info("releasing orphaned allocation",
				"network", network, "instance_id", allocation.InstanceID, "ip", allocation.IP)
		}

		if len(kept) == len(t.allocations) {
			continue
		}
		if err := m.save(network, kept); err != nil {
			return released, err
		}
	}

	return released, nil
}
