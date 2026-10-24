// Package reffw drives devices running the LED Link reference firmware — open
// panel controllers (ESP32 / Raspberry Pi) that expose a small HTTP API and
// receive pixels over DDP. This is what makes bare HUB75 panels plug-and-play:
// `ledlink identify` recognizes them and connects with the right settings.
//
// The firmware contract is specified in docs/reference-firmware.md.
// Address syntax: "host[:port]" (HTTP control port, default 8080).
package reffw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/ddpwire"
	"adplot/ledlink/internal/render"
)

const (
	// DriverName is the adapter name registered in the core registry.
	DriverName = "reffw"
	// DefaultHTTPPort is the reference firmware's control port.
	DefaultHTTPPort = 8080

	infoPath       = "/ledlink/info"
	brightnessPath = "/ledlink/brightness"
	powerPath      = "/ledlink/power"
)

// Info is the JSON document served by the firmware at /ledlink/info.
type Info struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Version string `json:"firmware"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	DDPPort int    `json:"ddp_port"`
}

// Driver implements core.Driver for the LED Link reference firmware.
type Driver struct{}

// New returns a reference-firmware driver.
func New() *Driver { return &Driver{} }

// Name implements core.Driver.
func (d *Driver) Name() string { return DriverName }

// Probe implements core.Driver by asking the firmware for its info document.
func (d *Driver) Probe(ctx context.Context, addr string) core.ProbeResult {
	if _, err := fetchInfo(ctx, addr); err != nil {
		return core.ProbeResult{}
	}
	return core.ProbeResult{Reachable: true, Protocol: "reffw", Detail: "LED Link reference firmware"}
}

// Connect implements core.Driver.
func (d *Driver) Connect(ctx context.Context, addr string) (core.Device, error) {
	fwInfo, err := fetchInfo(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("reffw %s: %w", addr, err)
	}
	base, err := baseURL(addr)
	if err != nil {
		return nil, err
	}
	host, _ := splitHostPort(addr, DefaultHTTPPort)
	conn, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(fwInfo.DDPPort)))
	if err != nil {
		return nil, fmt.Errorf("reffw %s: ddp dial: %w", addr, err)
	}

	id := DriverName + "@" + addr
	name := fwInfo.Name
	if name == "" {
		name = "refpanel"
	}
	model := fwInfo.Model
	if model == "" {
		model = "Reference firmware"
	}
	return &device{
		conn:     conn,
		id:       id,
		addr:     addr,
		httpBase: base,
		fwInfo:   fwInfo,
		client:   &http.Client{Timeout: 5 * time.Second},
		info: core.DeviceInfo{
			ID:           id,
			Name:         name,
			Driver:       DriverName,
			Vendor:       "LED Link",
			Model:        model,
			Address:      addr,
			ScreenWidth:  fwInfo.Width,
			ScreenHeight: fwInfo.Height,
			Firmware:     fwInfo.Version,
			Protocol:     "reffw/ddp",
			Capabilities: []string{"text", "brightness", "power"},
		},
	}, nil
}

// fetchInfo retrieves and validates the firmware's info document.
func fetchInfo(ctx context.Context, addr string) (Info, error) {
	base, err := baseURL(addr)
	if err != nil {
		return Info{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+infoPath, nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("GET %s returned %d", infoPath, resp.StatusCode)
	}
	var info Info
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("invalid %s document: %w", infoPath, err)
	}
	if info.Width < 1 || info.Width > 8192 || info.Height < 1 || info.Height > 8192 {
		return Info{}, fmt.Errorf("invalid panel size %dx%d in %s", info.Width, info.Height, infoPath)
	}
	if info.DDPPort == 0 {
		info.DDPPort = ddpwire.DefaultPort
	}
	return info, nil
}

// baseURL turns "host[:port]" into "http://host:port".
func baseURL(addr string) (string, error) {
	host, port := splitHostPort(addr, DefaultHTTPPort)
	if host == "" {
		return "", fmt.Errorf("reffw address %q must include a host", addr)
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// splitHostPort parses "host" or "host:port", falling back to defaultPort.
func splitHostPort(addr string, defaultPort int) (string, int) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return strings.TrimSpace(addr), defaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, defaultPort
	}
	return host, port
}

type device struct {
	mu       sync.Mutex
	conn     net.Conn
	enc      ddpwire.Encoder
	id       string
	addr     string
	httpBase string
	fwInfo   Info
	client   *http.Client
	info     core.DeviceInfo
	closed   bool
}

// Driver implements core.Device.
func (d *device) Driver() string { return DriverName }

// ID implements core.Device.
func (d *device) ID() string { return d.id }

// Info implements core.Device.
func (d *device) Info(ctx context.Context) core.DeviceInfo { return d.info }

// SendText implements core.Device by rasterizing the text and streaming it
// over DDP; the firmware applies its own brightness and power state.
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
	frame := render.RGBBytes(render.Fit(img, d.fwInfo.Width, d.fwInfo.Height))

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("device %s is closed", d.id)
	}
	for _, pkt := range d.enc.Packets(frame) {
		if _, err := d.conn.Write(pkt); err != nil {
			return fmt.Errorf("reffw %s: send frame: %w", d.addr, err)
		}
	}
	return nil
}

// UploadMedia implements core.Device. The reference firmware streams frames
// and has no storage; playlists need a vendor driver.
func (d *device) UploadMedia(ctx context.Context, paths []string) ([]string, error) {
	return nil, fmt.Errorf("reference firmware streams frames and has no storage; send text or use a vendor driver for playlists")
}

// SetBrightness implements core.Device via the firmware control API.
func (d *device) SetBrightness(ctx context.Context, percent int) error {
	if percent < 1 || percent > 100 {
		return fmt.Errorf("brightness must be between 1 and 100, got %d", percent)
	}
	return d.post(ctx, brightnessPath, map[string]int{"percent": percent})
}

// ScreenPower implements core.Device via the firmware control API.
func (d *device) ScreenPower(ctx context.Context, on bool) error {
	return d.post(ctx, powerPath, map[string]bool{"on": on})
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

// post sends a JSON body to one of the firmware control endpoints.
func (d *device) post(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.httpBase+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("reffw %s: %w", d.addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("reffw %s: %s returned %d", d.addr, path, resp.StatusCode)
	}
	return nil
}
