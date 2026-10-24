// Package ddp implements a driver for the Distributed Display Protocol (DDP),
// the open UDP pixel-streaming standard spoken by Falcon Player, WLED,
// ESPixelStick and custom ESP32 / Raspberry Pi LED controllers. It gives
// adplot a vendor-neutral path to any HUB75 panel with an open receiver.
//
// Address syntax: "host", "host:port", optionally with the pixel count:
// "192.168.1.50?w=64&h=32" (defaults: port 4048, 64x32 pixels).
package ddp

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/ddpwire"
	"adplot/ledlink/internal/render"
)

const (
	// DriverName is the adapter name registered in the core registry.
	DriverName = "ddp"
	// DefaultPort is the standard DDP UDP port.
	DefaultPort = ddpwire.DefaultPort
)

// Driver implements core.Driver for DDP pixel receivers.
type Driver struct{}

// New returns a DDP driver.
func New() *Driver { return &Driver{} }

// Name implements core.Driver.
func (d *Driver) Name() string { return DriverName }

// Probe implements core.Driver. DDP has no handshake, so receivers cannot be
// discovered by probing — connect explicitly and state the pixel count:
// {"driver":"ddp","address":"192.168.1.50?w=64&h=32"}.
func (d *Driver) Probe(ctx context.Context, addr string) core.ProbeResult {
	return core.ProbeResult{Detail: "passive protocol (no handshake); connect explicitly, e.g. host?w=64&h=32"}
}

// Connect implements core.Driver.
func (d *Driver) Connect(ctx context.Context, addr string) (core.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host, port, w, h, err := parseAddr(addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("ddp dial %s: %w", addr, err)
	}

	id := DriverName + "@" + addr
	return &device{
		conn:       conn,
		id:         id,
		addr:       addr,
		w:          w,
		h:          h,
		brightness: 100,
		poweredOn:  true,
		info: core.DeviceInfo{
			ID:           id,
			Name:         fmt.Sprintf("DDP %dx%d", w, h),
			Driver:       DriverName,
			Vendor:       "Open",
			Model:        "DDP pixel receiver",
			Address:      addr,
			ScreenWidth:  w,
			ScreenHeight: h,
			Protocol:     "ddp",
			Capabilities: []string{"text", "brightness", "power"},
		},
	}, nil
}

// parseAddr splits "host[:port][?w=N&h=M]" into its parts.
func parseAddr(addr string) (host string, port, w, h int, err error) {
	raw := strings.TrimSpace(addr)
	query := ""
	if i := strings.Index(raw, "?"); i >= 0 {
		raw, query = raw[:i], raw[i+1:]
	}

	host, port = raw, DefaultPort
	if hp, ps, splitErr := net.SplitHostPort(raw); splitErr == nil {
		host = hp
		if p, convErr := strconv.Atoi(ps); convErr == nil && p > 0 {
			port = p
		}
	}
	if host == "" {
		return "", 0, 0, 0, fmt.Errorf("ddp address %q must include a host", addr)
	}

	w, h = 64, 32
	if query != "" {
		values, qErr := url.ParseQuery(query)
		if qErr != nil {
			return "", 0, 0, 0, fmt.Errorf("invalid ddp options %q: %w", query, qErr)
		}
		if v := values.Get("w"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return "", 0, 0, 0, fmt.Errorf("invalid w=%q in ddp address", v)
			}
			w = n
		}
		if v := values.Get("h"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return "", 0, 0, 0, fmt.Errorf("invalid h=%q in ddp address", v)
			}
			h = n
		}
	}
	if w < 1 || w > 8192 || h < 1 || h > 8192 {
		return "", 0, 0, 0, fmt.Errorf("ddp screen size %dx%d out of range (1..8192)", w, h)
	}
	return host, port, w, h, nil
}

type device struct {
	mu         sync.Mutex
	conn       net.Conn
	id         string
	addr       string
	w, h       int
	brightness int
	poweredOn  bool
	enc        ddpwire.Encoder
	lastFrame  []byte // raw RGB frame (before brightness), nil until first content
	info       core.DeviceInfo
	closed     bool
}

// Driver implements core.Device.
func (d *device) Driver() string { return DriverName }

// ID implements core.Device.
func (d *device) ID() string { return d.id }

// Info implements core.Device.
func (d *device) Info(ctx context.Context) core.DeviceInfo { return d.info }

// SendText implements core.Device by rasterizing the text and streaming it as
// an RGB frame.
func (d *device) SendText(ctx context.Context, spec core.TextSpec) error {
	if strings.TrimSpace(spec.Text) == "" {
		return fmt.Errorf("text must not be empty")
	}
	col, err := render.ParseColor(spec.Color)
	if err != nil {
		return err
	}
	scale := 1
	if spec.FontSize > 0 {
		if s := spec.FontSize / 13; s > 1 {
			scale = s
		}
	}
	img := render.Text(spec.Text, col, scale)
	frame := render.RGBBytes(render.Fit(img, d.w, d.h))

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	return d.sendFrameLocked(frame)
}

// UploadMedia implements core.Device. DDP has no device storage; content is
// streamed frame by frame.
func (d *device) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	return nil, fmt.Errorf("ddp streams frames over UDP and has no device storage; send text or use a vendor driver for playlists")
}

// SetBrightness implements core.Device. DDP has no hardware brightness, so
// frames are scaled in software and the last frame is re-sent.
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
	if d.lastFrame != nil && d.poweredOn {
		return d.sendPixelsLocked(ddpwire.ApplyBrightness(d.lastFrame, d.brightness))
	}
	return nil
}

// ScreenPower implements core.Device. Powering off streams a black frame;
// powering on replays the last content.
func (d *device) ScreenPower(ctx context.Context, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	if on {
		d.poweredOn = true
		if d.lastFrame != nil {
			return d.sendPixelsLocked(ddpwire.ApplyBrightness(d.lastFrame, d.brightness))
		}
		return nil
	}
	d.poweredOn = false
	return d.sendPixelsLocked(make([]byte, d.w*d.h*3))
}

// Close implements core.Device.
func (d *device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.conn.Close()
}

// sendFrameLocked stores frame as the current content and streams it when the
// screen is on. Callers must hold d.mu.
func (d *device) sendFrameLocked(frame []byte) error {
	d.lastFrame = frame
	if !d.poweredOn {
		return nil
	}
	return d.sendPixelsLocked(ddpwire.ApplyBrightness(frame, d.brightness))
}

// sendPixelsLocked encodes data into DDP packets and writes them to the
// receiver. Callers must hold d.mu.
func (d *device) sendPixelsLocked(data []byte) error {
	for _, pkt := range d.enc.Packets(data) {
		if _, err := d.conn.Write(pkt); err != nil {
			return fmt.Errorf("ddp send to %s: %w", d.addr, err)
		}
	}
	return nil
}
