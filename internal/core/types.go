// Package core defines the vendor-neutral model shared by every LED driver
// adapter in LED Link.
package core

// DeviceInfo is the vendor-neutral description of a screen controller.
type DeviceInfo struct {
	// ID uniquely identifies the device within one LED Link instance.
	ID string `json:"id"`
	// Name is a human friendly name (device reported or user assigned).
	Name string `json:"name,omitempty"`
	// Driver is the adapter name, e.g. "huidu".
	Driver string `json:"driver"`
	// Vendor is the manufacturer family, e.g. "Huidu".
	Vendor string `json:"vendor"`
	// Model is the hardware model when known, e.g. "HD-WF2".
	Model string `json:"model,omitempty"`
	// Address is the host[:port] used to reach the device.
	Address string `json:"address"`
	// ScreenWidth and ScreenHeight are the configured screen size in pixels.
	ScreenWidth  int `json:"screen_width"`
	ScreenHeight int `json:"screen_height"`
	// Firmware is the controller firmware version when known.
	Firmware string `json:"firmware,omitempty"`
	// Protocol is the wire protocol family as reported by the adapter.
	Protocol string `json:"protocol,omitempty"`
	// Capabilities lists the supported operations, e.g. "text", "video".
	Capabilities []string `json:"capabilities,omitempty"`
}

// TextSpec describes a full-screen text message.
type TextSpec struct {
	Text     string `json:"text"`
	Color    string `json:"color,omitempty"`     // "#RRGGBB"
	FontName string `json:"font_name,omitempty"` // font installed on the controller
	FontSize int    `json:"font_size,omitempty"` // pixels
	Bold     bool   `json:"bold,omitempty"`
	Align    string `json:"align,omitempty"` // "left", "center" (default) or "right"
	// Effect is the vendor-neutral transition name, e.g. "fade",
	// "left-scroll-loop", "up-scroll-loop", "left-move". Empty means instant.
	Effect string `json:"effect,omitempty"`
	// Speed is the effect speed from 1 (slow) to 10 (fast).
	Speed int `json:"speed,omitempty"`
	// Duration is the display time in seconds. 0 lets the device decide.
	Duration int `json:"duration,omitempty"`
}

// ProbeResult reports whether a candidate address speaks a driver's protocol.
type ProbeResult struct {
	Reachable bool   `json:"reachable"`
	Protocol  string `json:"protocol,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ScanResult combines a probe result with the probed address.
type ScanResult struct {
	Driver    string `json:"driver"`
	Address   string `json:"address"`
	Reachable bool   `json:"reachable"`
	Protocol  string `json:"protocol,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
