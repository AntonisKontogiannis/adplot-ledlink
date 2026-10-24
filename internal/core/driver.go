package core

import "context"

// Driver is implemented by every vendor adapter (Huidu, Onbon, Colorlight, ...).
// Drivers are stateless with respect to devices: they create a Device per
// Connect call. Implementations must be safe for concurrent use.
type Driver interface {
	// Name returns the adapter name used in API calls, e.g. "huidu".
	Name() string

	// Probe checks quickly whether addr ("host" or "host:port") looks like a
	// device of this family. Probe must not block longer than ctx allows and
	// never returns an error: unreachable candidates simply report
	// Reachable=false.
	Probe(ctx context.Context, addr string) ProbeResult

	// Connect opens a session with the device at addr.
	Connect(ctx context.Context, addr string) (Device, error)
}

// Device is a live session with an LED screen controller. Implementations must
// be safe for concurrent use.
type Device interface {
	// Driver returns the name of the adapter that created the device.
	Driver() string
	// ID returns the unique id used to address the device through the API.
	ID() string
	// Info returns the device description captured when the session opened.
	Info(ctx context.Context) DeviceInfo
	// SendText pushes a full-screen text program to the device.
	SendText(ctx context.Context, spec TextSpec) error
	// UploadMedia copies media files (images, videos) to the device storage.
	// The returned names are the on-device file names, in input order.
	UploadMedia(ctx context.Context, paths []string) ([]string, error)
	// SetBrightness sets screen brightness in percent (1-100).
	SetBrightness(ctx context.Context, percent int) error
	// ScreenPower switches the screen output on or off.
	ScreenPower(ctx context.Context, on bool) error
	// Close terminates the session and releases resources.
	Close() error
}
