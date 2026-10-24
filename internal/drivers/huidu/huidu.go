// Package huidu adapts github.com/alparslanahmed/huidu-led to the LED Link
// driver interface. It speaks the Huidu SDK 2.0 binary TCP protocol used by
// HDPlayer-compatible full-color controller cards (for example HD-WF2 and the
// A/C/D series) over WiFi or Ethernet.
package huidu

import (
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	huidu "github.com/alparslanahmed/huidu-led"

	"adplot/ledlink/internal/core"
)

// DriverName is the adapter name registered in the core registry.
const DriverName = "huidu"

// Driver implements core.Driver.
type Driver struct{}

// New returns a Huidu driver.
func New() *Driver { return &Driver{} }

// Name implements core.Driver.
func (d *Driver) Name() string { return DriverName }

// Probe implements core.Driver by running the library's TCP/UDP protocol probe.
func (d *Driver) Probe(ctx context.Context, addr string) core.ProbeResult {
	host, port := splitAddr(addr)

	timeout := 800 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	res := huidu.ProbeProtocol(host, port, timeout)
	out := core.ProbeResult{}
	switch {
	case res.TCP.Reachable:
		out.Reachable = true
		out.Protocol = string(res.TCP.Protocol)
		out.Detail = "tcp"
	case res.UDP.Reachable:
		out.Reachable = true
		out.Protocol = string(res.UDP.Protocol)
		out.Detail = "udp"
	}
	return out
}

// Connect implements core.Driver.
func (d *Driver) Connect(ctx context.Context, addr string) (core.Device, error) {
	host, port := splitAddr(addr)

	dev := huidu.NewDevice(host, port,
		huidu.WithTimeout(10*time.Second),
		huidu.WithHeartbeatInterval(30*time.Second),
		huidu.WithAutoReconnect(true),
		huidu.WithLogger(debugLogger{}),
	)
	if err := call(ctx, dev.Connect); err != nil {
		return nil, fmt.Errorf("huidu %s: handshake: %w", addr, err)
	}

	info, err := dev.GetDeviceInfo()
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("huidu %s: get device info: %w", addr, err)
	}

	id := info.DeviceID
	if id == "" {
		id = DriverName + "@" + addr
	}
	return &device{
		dev:  dev,
		id:   id,
		addr: addr,
		info: core.DeviceInfo{
			ID:           id,
			Name:         info.DeviceName,
			Driver:       DriverName,
			Vendor:       "Huidu",
			Model:        info.Model,
			Address:      addr,
			ScreenWidth:  info.ScreenWidth,
			ScreenHeight: info.ScreenHeight,
			Firmware:     info.AppVersion,
			Protocol:     string(dev.Protocol()),
			Capabilities: []string{"text", "image", "video", "clock", "brightness", "power", "time-sync"},
		},
	}, nil
}

type device struct {
	dev  *huidu.Device
	id   string
	addr string
	info core.DeviceInfo
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
	effect, err := mapEffect(spec.Effect)
	if err != nil {
		return err
	}
	hAlign, err := mapAlign(spec.Align)
	if err != nil {
		return err
	}

	cfg := huidu.TextConfig{
		FontName: spec.FontName,
		FontSize: spec.FontSize,
		Color:    spec.Color,
		Bold:     spec.Bold,
		HAlign:   hAlign,
		Effect:   effect,
		Speed:    spec.Speed,
		Duration: spec.Duration,
	}
	if err := call(ctx, func() error { return d.dev.SendText(spec.Text, cfg) }); err != nil {
		return fmt.Errorf("huidu %s: send text: %w", d.addr, err)
	}
	return nil
}

// UploadMedia implements core.Device.
func (d *device) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no files given")
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := call(ctx, func() error { return d.dev.UploadFile(path) }); err != nil {
			return names, fmt.Errorf("huidu %s: upload %s: %w", d.addr, path, err)
		}
		names = append(names, filepath.Base(path))
	}
	return names, nil
}

// SetBrightness implements core.Device.
func (d *device) SetBrightness(ctx context.Context, percent int) error {
	if percent < 1 || percent > 100 {
		return fmt.Errorf("brightness must be between 1 and 100, got %d", percent)
	}
	if err := call(ctx, func() error { return d.dev.SetBrightness(percent) }); err != nil {
		return fmt.Errorf("huidu %s: set brightness: %w", d.addr, err)
	}
	return nil
}

// ScreenPower implements core.Device.
func (d *device) ScreenPower(ctx context.Context, on bool) error {
	fn := d.dev.CloseScreen
	if on {
		fn = d.dev.OpenScreen
	}
	if err := call(ctx, fn); err != nil {
		return fmt.Errorf("huidu %s: screen power: %w", d.addr, err)
	}
	return nil
}

// Close implements core.Device.
func (d *device) Close() error { return d.dev.Close() }

// call runs fn while honoring ctx cancellation. The protocol call itself is
// synchronous; if ctx expires first, the call keeps running in the background
// until its own I/O timeout ends it.
func call(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// splitAddr parses "host" or "host:port" and falls back to the Huidu default
// port (10001).
func splitAddr(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, huidu.DefaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, huidu.DefaultPort
	}
	return host, port
}

// mapEffect converts a vendor-neutral effect name to the Huidu effect enum.
func mapEffect(name string) (huidu.EffectType, error) {
	if strings.TrimSpace(name) == "" {
		return huidu.EffectImmediate, nil
	}
	effect, ok := effectMap[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return 0, fmt.Errorf("unknown effect %q", name)
	}
	return effect, nil
}

var effectMap = map[string]huidu.EffectType{
	"none":              huidu.EffectImmediate,
	"immediate":         huidu.EffectImmediate,
	"fade":              huidu.EffectFade,
	"fade-in":           huidu.EffectFade,
	"left-move":         huidu.EffectLeftMove,
	"right-move":        huidu.EffectRightMove,
	"up-move":           huidu.EffectUpMove,
	"down-move":         huidu.EffectDownMove,
	"left-scroll":       huidu.EffectLeftScroll,
	"right-scroll":      huidu.EffectRightScroll,
	"up-scroll":         huidu.EffectUpScroll,
	"down-scroll":       huidu.EffectDownScroll,
	"left-scroll-loop":  huidu.EffectLeftScrollLoop,
	"right-scroll-loop": huidu.EffectRightScrollLoop,
	"up-scroll-loop":    huidu.EffectUpScrollLoop,
	"down-scroll-loop":  huidu.EffectDownScrollLoop,
	"random":            huidu.EffectRandom,
}

// mapAlign converts a vendor-neutral alignment to the Huidu alignment enum.
func mapAlign(name string) (huidu.HAlign, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return huidu.HAlignCenter, nil
	case "left":
		return huidu.HAlignLeft, nil
	case "center":
		return huidu.HAlignCenter, nil
	case "right":
		return huidu.HAlignRight, nil
	default:
		return "", fmt.Errorf("unknown alignment %q", name)
	}
}

// debugLogger adapts the standard library logger to the huidu.Logger
// interface, routing library diagnostics to stderr.
type debugLogger struct{}

// Printf implements huidu.Logger.
func (debugLogger) Printf(format string, v ...interface{}) {
	log.Printf("[huidu] "+format, v...)
}
