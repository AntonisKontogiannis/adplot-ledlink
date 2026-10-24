package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/drivers/mock"
)

func newTestManager() *core.Manager {
	reg := core.NewRegistry()
	reg.Register(mock.New())
	return core.NewManager(reg)
}

func TestIdentify(t *testing.T) {
	m := newTestManager()
	results := m.Identify(context.Background(), "mock-2", 500*time.Millisecond)
	if len(results) != 1 {
		t.Fatalf("want 1 driver result, got %d", len(results))
	}
	if !results[0].Reachable || results[0].Driver != "mock" {
		t.Fatalf("unexpected identify result: %+v", results[0])
	}
}

func TestConnectAuto(t *testing.T) {
	m := newTestManager()
	info, err := m.ConnectAuto(context.Background(), "mock-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Driver != "mock" || info.ID != "mock-1" {
		t.Fatalf("unexpected device info: %+v", info)
	}
}

func TestConnectAutoNoMatch(t *testing.T) {
	m := newTestManager()
	_, err := m.ConnectAuto(context.Background(), "10.255.255.1")
	if !errors.Is(err, core.ErrNoDriverMatch) {
		t.Fatalf("want ErrNoDriverMatch, got %v", err)
	}
}

func TestScanAll(t *testing.T) {
	m := newTestManager()
	results, err := m.ScanAll(context.Background(), []string{"mock-1", "mock-2"}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
}

// fakeDevice is a minimal core.Device used by the fake driver below.
type fakeDevice struct {
	id     string
	driver string
}

func (d *fakeDevice) Driver() string { return d.driver }
func (d *fakeDevice) ID() string     { return d.id }
func (d *fakeDevice) Info(ctx context.Context) core.DeviceInfo {
	return core.DeviceInfo{ID: d.id, Driver: d.driver}
}
func (d *fakeDevice) SendText(ctx context.Context, spec core.TextSpec) error { return nil }
func (d *fakeDevice) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	return nil, nil
}
func (d *fakeDevice) SetBrightness(ctx context.Context, percent int) error { return nil }
func (d *fakeDevice) ScreenPower(ctx context.Context, on bool) error       { return nil }
func (d *fakeDevice) Close() error                                         { return nil }

// fakeDriver reports a canned probe result and optionally fails to connect.
type fakeDriver struct {
	name       string
	probe      core.ProbeResult
	connectErr error
}

func (f *fakeDriver) Name() string { return f.name }
func (f *fakeDriver) Probe(ctx context.Context, addr string) core.ProbeResult {
	return f.probe
}
func (f *fakeDriver) Connect(ctx context.Context, addr string) (core.Device, error) {
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	return &fakeDevice{id: f.name + "-dev", driver: f.name}, nil
}

func TestConnectAutoPrefersRecognizedProtocol(t *testing.T) {
	reg := core.NewRegistry()
	// "aweak" sorts first and reports an open port only; "zstrong" recognized
	// the protocol and must win even though it sorts later.
	reg.Register(&fakeDriver{
		name:  "aweak",
		probe: core.ProbeResult{Reachable: true, Protocol: "unknown"},
	})
	reg.Register(&fakeDriver{
		name:  "zstrong",
		probe: core.ProbeResult{Reachable: true, Protocol: "recognized"},
	})
	m := core.NewManager(reg)

	info, err := m.ConnectAuto(context.Background(), "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if info.Driver != "zstrong" {
		t.Fatalf("connected via %q, want zstrong", info.Driver)
	}
}

func TestConnectAutoFallsThroughFailedHandshake(t *testing.T) {
	reg := core.NewRegistry()
	reg.Register(&fakeDriver{
		name:       "fails",
		probe:      core.ProbeResult{Reachable: true, Protocol: "recognized"},
		connectErr: errors.New("handshake failed"),
	})
	reg.Register(&fakeDriver{
		name:  "works",
		probe: core.ProbeResult{Reachable: true, Protocol: "unknown"},
	})
	m := core.NewManager(reg)

	info, err := m.ConnectAuto(context.Background(), "192.0.2.11")
	if err != nil {
		t.Fatal(err)
	}
	if info.Driver != "works" {
		t.Fatalf("connected via %q, want works", info.Driver)
	}
}
