package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// scanWorkers controls how many probes run in parallel during Scan.
const scanWorkers = 32

// Manager owns the driver registry and the set of connected devices.
type Manager struct {
	Registry *Registry

	mu      sync.RWMutex
	devices map[string]Device
}

// NewManager returns a Manager using the given registry.
func NewManager(reg *Registry) *Manager {
	return &Manager{Registry: reg, devices: make(map[string]Device)}
}

// Connect opens a session through driverName and registers the device.
func (m *Manager) Connect(ctx context.Context, driverName, addr string) (DeviceInfo, error) {
	d, err := m.Registry.Get(driverName)
	if err != nil {
		return DeviceInfo{}, err
	}
	dev, err := d.Connect(ctx, addr)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("connect via %s to %s: %w", driverName, addr, err)
	}

	m.mu.Lock()
	id := dev.ID()
	if id == "" {
		id = driverName + "@" + addr
	}
	if _, exists := m.devices[id]; exists {
		m.mu.Unlock()
		dev.Close()
		return DeviceInfo{}, fmt.Errorf("%w: %q", ErrAlreadyConnected, id)
	}
	m.devices[id] = dev
	m.mu.Unlock()

	return dev.Info(ctx), nil
}

// List returns the info of every connected device.
func (m *Manager) List() []DeviceInfo {
	m.mu.RLock()
	devs := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		devs = append(devs, d)
	}
	m.mu.RUnlock()

	infos := make([]DeviceInfo, 0, len(devs))
	for _, d := range devs {
		infos = append(infos, d.Info(context.Background()))
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos
}

// Get returns the connected device with the given id.
func (m *Manager) Get(id string) (Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	return d, ok
}

// Disconnect closes and unregisters the device with the given id.
func (m *Manager) Disconnect(id string) error {
	m.mu.Lock()
	dev, ok := m.devices[id]
	if ok {
		delete(m.devices, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("device %q not found", id)
	}
	return dev.Close()
}

// Scan probes every target with driverName in parallel and returns the
// reachable devices. Targets are host names or IP literals; CIDR expansion is
// done by internal/discovery.
func (m *Manager) Scan(ctx context.Context, driverName string, targets []string, timeout time.Duration) ([]ScanResult, error) {
	d, err := m.Registry.Get(driverName)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}

	var (
		mu      sync.Mutex
		results []ScanResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, scanWorkers)
	)

	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()

			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			pr := d.Probe(pctx, target)
			if !pr.Reachable {
				return
			}
			mu.Lock()
			results = append(results, ScanResult{
				Driver:    driverName,
				Address:   target,
				Reachable: true,
				Protocol:  pr.Protocol,
				Detail:    pr.Detail,
			})
			mu.Unlock()
		}(target)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Address < results[j].Address })
	return results, nil
}

// identifyTimeout bounds each driver probe during Identify.
const identifyTimeout = 2 * time.Second

// IdentifyResult is one driver's opinion about a candidate address.
type IdentifyResult struct {
	Driver    string `json:"driver"`
	Reachable bool   `json:"reachable"`
	Protocol  string `json:"protocol,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Identify probes addr with every registered driver and reports what each of
// them found. This is the generic "what is this device?" entry point: the
// caller does not need to know the vendor family in advance.
func (m *Manager) Identify(ctx context.Context, addr string, timeout time.Duration) []IdentifyResult {
	if timeout <= 0 {
		timeout = identifyTimeout
	}
	names := m.Registry.Names()
	results := make([]IdentifyResult, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		d, err := m.Registry.Get(name)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(i int, name string, d Driver) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			pr := d.Probe(pctx, addr)
			results[i] = IdentifyResult{
				Driver:    name,
				Reachable: pr.Reachable,
				Protocol:  pr.Protocol,
				Detail:    pr.Detail,
			}
		}(i, name, d)
	}
	wg.Wait()

	found := make([]IdentifyResult, 0, len(results))
	for _, res := range results {
		if res.Driver != "" {
			found = append(found, res)
		}
	}
	return found
}

// ConnectAuto identifies addr and connects with the best matching driver:
// drivers that recognized the protocol are tried before drivers that merely
// saw an open port, and failed handshakes fall through to the next candidate.
func (m *Manager) ConnectAuto(ctx context.Context, addr string) (DeviceInfo, error) {
	matches := m.Identify(ctx, addr, identifyTimeout)

	candidates := make([]IdentifyResult, 0, len(matches))
	for _, strong := range []bool{true, false} {
		for _, res := range matches {
			if !res.Reachable {
				continue
			}
			recognized := res.Protocol != "" && res.Protocol != "unknown"
			if recognized == strong {
				candidates = append(candidates, res)
			}
		}
	}
	if len(candidates) == 0 {
		return DeviceInfo{}, fmt.Errorf("%w: %s", ErrNoDriverMatch, addr)
	}

	var lastErr error
	for _, res := range candidates {
		info, err := m.Connect(ctx, res.Driver, addr)
		if err == nil {
			return info, nil
		}
		lastErr = err
	}
	return DeviceInfo{}, fmt.Errorf("auto-connect %s: no driver could open a session: %w", addr, lastErr)
}

// ScanAll probes every target with every registered driver and merges the
// results. A device may appear more than once if several drivers recognize it.
func (m *Manager) ScanAll(ctx context.Context, targets []string, timeout time.Duration) ([]ScanResult, error) {
	var all []ScanResult
	for _, name := range m.Registry.Names() {
		results, err := m.Scan(ctx, name, targets, timeout)
		if err != nil {
			return nil, err
		}
		all = append(all, results...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Address != all[j].Address {
			return all[i].Address < all[j].Address
		}
		return all[i].Driver < all[j].Driver
	})
	return all, nil
}
