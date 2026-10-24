// Package mock implements an in-memory LED Link driver that simulates screen
// controllers. It lets adplot integrate against LED Link before real hardware
// is available, and it powers the automated tests.
package mock

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"adplot/ledlink/internal/core"
)

// DriverName is the adapter name registered in the core registry.
const DriverName = "mock"

// Driver implements core.Driver. Any address starting with "mock" is accepted:
// "mock-2" simulates a 192x96 screen, every other "mock-N" a 128x64 screen.
type Driver struct{}

// New returns a mock driver.
func New() *Driver { return &Driver{} }

// Name implements core.Driver.
func (d *Driver) Name() string { return DriverName }

// Probe implements core.Driver.
func (d *Driver) Probe(ctx context.Context, addr string) core.ProbeResult {
	if isMockAddr(addr) {
		return core.ProbeResult{Reachable: true, Protocol: "simulated", Detail: "mock device"}
	}
	return core.ProbeResult{}
}

// Connect implements core.Driver.
func (d *Driver) Connect(ctx context.Context, addr string) (core.Device, error) {
	if !isMockAddr(addr) {
		return nil, fmt.Errorf("mock driver only handles addresses starting with %q, got %q", "mock", addr)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(50 * time.Millisecond): // simulate the connection handshake
	}

	width, height := 128, 64
	if strings.HasSuffix(addr, "-2") {
		width, height = 192, 96
	}
	return &device{
		id: addr,
		info: core.DeviceInfo{
			ID:           addr,
			Name:         addr,
			Driver:       DriverName,
			Vendor:       "Mock",
			Model:        "MockBox",
			Address:      addr,
			ScreenWidth:  width,
			ScreenHeight: height,
			Firmware:     "mock-1.0",
			Protocol:     "simulated",
			Capabilities: []string{"text", "image", "video", "brightness", "power"},
		},
		brightness: 80,
		poweredOn:  true,
	}, nil
}

func isMockAddr(addr string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(addr)), "mock")
}

type device struct {
	mu         sync.Mutex
	id         string
	info       core.DeviceInfo
	brightness int
	poweredOn  bool
	lastText   string
	uploads    []string
	closed     bool
}

// Driver implements core.Device.
func (d *device) Driver() string { return DriverName }

// ID implements core.Device.
func (d *device) ID() string { return d.id }

// Info implements core.Device.
func (d *device) Info(ctx context.Context) core.DeviceInfo { return d.info }

// SendText implements core.Device.
func (d *device) SendText(ctx context.Context, spec core.TextSpec) error {
	if strings.TrimSpace(spec.Text) == "" {
		return fmt.Errorf("text must not be empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	d.lastText = spec.Text
	return nil
}

// UploadMedia implements core.Device.
func (d *device) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no files given")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("device %s is closed", d.id)
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		name := filepath.Base(p)
		names = append(names, name)
		d.uploads = append(d.uploads, name)
	}
	return names, nil
}

// SetBrightness implements core.Device.
func (d *device) SetBrightness(ctx context.Context, percent int) error {
	if percent < 1 || percent > 100 {
		return fmt.Errorf("brightness must be between 1 and 100, got %d", percent)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	d.brightness = percent
	return nil
}

// ScreenPower implements core.Device.
func (d *device) ScreenPower(ctx context.Context, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	d.poweredOn = on
	return nil
}

// Close implements core.Device.
func (d *device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}
