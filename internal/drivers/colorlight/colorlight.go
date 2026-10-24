// Package colorlight drives Colorlight 5A-75x receiver cards (bare HUB75
// panels with no player box) by sending raw Ethernet frames from the host's
// network interface — the same mechanism Falcon Player uses. Protocol
// reference: docs/protocols.md (firmware 13.x notes, public domain).
//
// Address syntax: "<interface>[?w=64&h=32]", e.g. "enp3s0?w=320&h=256".
// Linux only for now; requires root or CAP_NET_RAW and a Gigabit link.
package colorlight

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/render"
)

const (
	// DriverName is the adapter name registered in the core registry.
	DriverName = "colorlight"

	// Fixed MACs required by the 5A-75x protocol.
	dstMAC = "\x11\x22\x33\x44\x55\x66"
	srcMAC = "\x22\x22\x33\x44\x55\x66"

	etherTypeSync       = 0x0101 // buffer swap
	etherTypeBrightness = 0x0AFF // brightness + frame start
	etherTypePixelBase  = 0x5500 // | row MSB

	maxPixelsPerPacket = 497 // (1500 - 7) / 3 per the protocol notes
	minEthernetFrame   = 60  // pad tiny frames to the Ethernet minimum

	brightnessPayloadLen = 63
	syncPayloadLen       = 98
)

// frameSender is the link-layer transport used to emit raw Ethernet frames.
type frameSender interface {
	Send(frame []byte) error
	Close() error
}

// Driver implements core.Driver for Colorlight 5A-75x receiver cards.
type Driver struct{}

// New returns a Colorlight driver.
func New() *Driver { return &Driver{} }

// Name implements core.Driver.
func (d *Driver) Name() string { return DriverName }

// Probe implements core.Driver. Colorlight cards are write-only on the wire —
// the discover packet would leave the card unresponsive until a power cycle —
// so they cannot be probed; connect explicitly with the network interface.
func (d *Driver) Probe(ctx context.Context, addr string) core.ProbeResult {
	return core.ProbeResult{Detail: "write-only raw-Ethernet protocol (no safe discovery); connect explicitly, e.g. enp3s0?w=64&h=32"}
}

// Connect implements core.Driver.
func (d *Driver) Connect(ctx context.Context, addr string) (core.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	iface, w, h, err := parseAddr(addr)
	if err != nil {
		return nil, err
	}
	sender, err := openRawSender(iface)
	if err != nil {
		return nil, err
	}

	id := fmt.Sprintf("%s@%s", DriverName, addr)
	return &device{
		sender:     sender,
		id:         id,
		addr:       addr,
		iface:      iface,
		w:          w,
		h:          h,
		brightness: 100,
		poweredOn:  true,
		info: core.DeviceInfo{
			ID:           id,
			Name:         fmt.Sprintf("Colorlight 5A-75x %dx%d", w, h),
			Driver:       DriverName,
			Vendor:       "Colorlight",
			Model:        "5A-75x receiver",
			Address:      addr,
			ScreenWidth:  w,
			ScreenHeight: h,
			Protocol:     "colorlight-v13",
			Capabilities: []string{"text", "brightness", "power"},
		},
	}, nil
}

// parseAddr splits "<interface>[?w=N&h=M]" into its parts.
func parseAddr(addr string) (iface string, w, h int, err error) {
	raw := strings.TrimSpace(addr)
	query := ""
	if i := strings.Index(raw, "?"); i >= 0 {
		raw, query = raw[:i], raw[i+1:]
	}
	if raw == "" {
		return "", 0, 0, fmt.Errorf("colorlight address %q must name a network interface, e.g. enp3s0?w=64&h=32", addr)
	}

	w, h = 64, 32
	if query != "" {
		values, qErr := url.ParseQuery(query)
		if qErr != nil {
			return "", 0, 0, fmt.Errorf("invalid colorlight options %q: %w", query, qErr)
		}
		if v := values.Get("w"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return "", 0, 0, fmt.Errorf("invalid w=%q in colorlight address", v)
			}
			w = n
		}
		if v := values.Get("h"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return "", 0, 0, fmt.Errorf("invalid h=%q in colorlight address", v)
			}
			h = n
		}
	}
	if w < 1 || w > 8192 || h < 1 || h > 8192 {
		return "", 0, 0, fmt.Errorf("colorlight screen size %dx%d out of range (1..8192)", w, h)
	}
	return raw, w, h, nil
}

type device struct {
	mu         sync.Mutex
	sender     frameSender
	id         string
	addr       string
	iface      string
	w, h       int
	brightness int
	poweredOn  bool
	lastFrame  []byte // raw RGB frame, nil until first content
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

// UploadMedia implements core.Device. Colorlight receivers have no storage;
// content is streamed frame by frame.
func (d *device) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	return nil, fmt.Errorf("colorlight streams frames over Ethernet and has no device storage; send text or use a vendor driver for playlists")
}

// SetBrightness implements core.Device. Brightness travels in the 0x0AFF
// frame-start packet, so the current frame is re-transmitted.
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
		return d.transmitLocked(d.lastFrame)
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
			return d.transmitLocked(d.lastFrame)
		}
		return nil
	}
	d.poweredOn = false
	return d.transmitLocked(make([]byte, d.w*d.h*3))
}

// Close implements core.Device.
func (d *device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.sender.Close()
}

// sendFrameLocked stores frame and streams it when the screen is on. Identical
// content is skipped to avoid needless buffer swaps. Callers must hold d.mu.
func (d *device) sendFrameLocked(frame []byte) error {
	unchanged := bytes.Equal(frame, d.lastFrame)
	d.lastFrame = frame
	if !d.poweredOn || unchanged {
		return nil
	}
	return d.transmitLocked(frame)
}

// transmitLocked emits the full firmware-13.x update sequence described in the
// protocol notes — brightness, rows, swap (×2), then brightness + rows again
// to fill the hidden buffer without swapping (flicker-free). Callers must hold
// d.mu.
func (d *device) transmitLocked(rgb []byte) error {
	if len(rgb) != d.w*d.h*3 {
		return fmt.Errorf("colorlight frame size %d bytes does not match %dx%d", len(rgb), d.w, d.h)
	}
	for _, pkt := range framePackets(rgb, d.w, d.h, d.brightness) {
		if err := d.sender.Send(padFrame(pkt)); err != nil {
			return fmt.Errorf("colorlight send on %s: %w", d.iface, err)
		}
	}
	return nil
}

// framePackets builds the complete update sequence: 2× brightness, row data,
// 2× sync/swap, then 2× brightness + row data again to fill the second buffer.
func framePackets(rgb []byte, w, h, brightness int) [][]byte {
	bri := brightnessPacket(brightness)
	syncPkt := syncPacket()
	rows := rowPackets(rgb, w, h)

	packets := make([][]byte, 0, 4+2*len(rows))
	packets = append(packets, bri, bri)
	packets = append(packets, rows...)
	packets = append(packets, syncPkt, syncPkt)
	packets = append(packets, bri, bri)
	packets = append(packets, rows...)
	return packets
}

// ethernetHeader returns dst MAC + src MAC + EtherType.
func ethernetHeader(etherType uint16) []byte {
	h := make([]byte, 0, 14)
	h = append(h, dstMAC...)
	h = append(h, srcMAC...)
	h = append(h, byte(etherType>>8), byte(etherType))
	return h
}

// brightnessPacket builds a 0x0AFF frame-start packet (63-byte payload).
func brightnessPacket(brightness int) []byte {
	level := byte(brightness * 255 / 100)
	payload := make([]byte, brightnessPayloadLen)
	payload[0], payload[1], payload[2] = level, level, level
	payload[3] = 0xFF
	return append(ethernetHeader(etherTypeBrightness), payload...)
}

// syncPacket builds a 0x0101 buffer-swap packet.
func syncPacket() []byte {
	return append(ethernetHeader(etherTypeSync), make([]byte, syncPayloadLen)...)
}

// rowPackets encodes every display row as one or more 0x5500 packets with BGR
// pixel data (the card expects BGR, our frames are RGB).
func rowPackets(rgb []byte, w, h int) [][]byte {
	var packets [][]byte
	for row := 0; row < h; row++ {
		rowStart := row * w * 3
		for off := 0; off < w; off += maxPixelsPerPacket {
			count := w - off
			if count > maxPixelsPerPacket {
				count = maxPixelsPerPacket
			}
			etherType := uint16(etherTypePixelBase) | uint16((row>>8)&0xFF)
			pkt := ethernetHeader(etherType)
			pkt = append(pkt,
				byte(row&0xFF),
				byte((off>>8)&0xFF),
				byte(off&0xFF),
				byte((count>>8)&0xFF),
				byte(count&0xFF),
				0x08,
				0x88,
			)
			for i := 0; i < count; i++ {
				px := rgb[rowStart+off*3+i*3 : rowStart+off*3+i*3+3]
				pkt = append(pkt, px[2], px[1], px[0]) // RGB -> BGR
			}
			packets = append(packets, pkt)
		}
	}
	return packets
}

// padFrame pads frames shorter than the Ethernet minimum (60 bytes).
func padFrame(frame []byte) []byte {
	if len(frame) >= minEthernetFrame {
		return frame
	}
	padded := make([]byte, minEthernetFrame)
	copy(padded, frame)
	return padded
}
