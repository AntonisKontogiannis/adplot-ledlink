package core

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sentinel errors used by the registry and the manager so that API layers can
// map them to HTTP status codes.
var (
	// ErrUnknownDriver is returned when a driver name was not registered.
	ErrUnknownDriver = errors.New("unknown driver")
	// ErrAlreadyConnected is returned when a device id is already registered.
	ErrAlreadyConnected = errors.New("device is already connected")
	// ErrNoDriverMatch is returned when auto-detection finds no driver that
	// recognizes a device address.
	ErrNoDriverMatch = errors.New("no registered driver recognized the device")
)

// Registry holds the available driver adapters.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

// NewRegistry returns an empty driver registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

// Register adds a driver under its Name. A later driver with the same name
// replaces the earlier one.
func (r *Registry) Register(d Driver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drivers[d.Name()] = d
}

// Get returns the driver registered under name.
func (r *Registry) Get(name string) (Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("%w %q (available: %v)", ErrUnknownDriver, name, r.namesLocked())
	}
	return d, nil
}

// Names returns the registered driver names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.namesLocked()
}

func (r *Registry) namesLocked() []string {
	names := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
