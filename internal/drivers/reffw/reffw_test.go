package reffw

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/ddpwire"
)

// mockFirmware implements the reference-firmware contract: HTTP info and
// control endpoints plus a DDP receiver.
type mockFirmware struct {
	srv  *httptest.Server
	pc   net.PacketConn
	port int
	w, h int

	mu         sync.Mutex
	brightness int
	powerOn    bool

	frames chan []byte
}

func startMockFirmware(t *testing.T, w, h int) *mockFirmware {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fw := &mockFirmware{
		pc:      pc,
		port:    pc.LocalAddr().(*net.UDPAddr).Port,
		w:       w,
		h:       h,
		powerOn: true,
		frames:  make(chan []byte, 8),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ledlink/info", func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"name": "mock-ref", "model": "MockRef", "firmware": "test",
			"width": w, "height": h, "ddp_port": fw.port,
		})
	})
	mux.HandleFunc("POST /ledlink/brightness", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Percent int `json:"percent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		fw.mu.Lock()
		fw.brightness = req.Percent
		fw.mu.Unlock()
		rw.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /ledlink/power", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		fw.mu.Lock()
		fw.powerOn = req.On
		fw.mu.Unlock()
		rw.WriteHeader(http.StatusOK)
	})

	fw.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		fw.srv.Close()
		fw.pc.Close()
	})
	go fw.receiveLoop()
	return fw
}

func (fw *mockFirmware) addr() string {
	return strings.TrimPrefix(fw.srv.URL, "http://")
}

// receiveLoop reassembles DDP packets into frames and emits completed frames.
func (fw *mockFirmware) receiveLoop() {
	buf := make([]byte, 65536)
	frame := make([]byte, fw.w*fw.h*3)
	for {
		n, _, err := fw.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < ddpwire.HeaderLen {
			continue
		}
		off := int(binary.BigEndian.Uint32(buf[4:8]))
		length := int(binary.BigEndian.Uint16(buf[8:10]))
		if off+length > len(frame) || n < ddpwire.HeaderLen+length {
			continue
		}
		copy(frame[off:], buf[ddpwire.HeaderLen:ddpwire.HeaderLen+length])
		if off+length == len(frame) {
			cp := make([]byte, len(frame))
			copy(cp, frame)
			select {
			case fw.frames <- cp:
			default:
			}
		}
	}
}

func connectDevice(t *testing.T, addr string) core.Device {
	t.Helper()
	dev, err := New().Connect(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	return dev
}

func TestProbeAndConnect(t *testing.T) {
	fw := startMockFirmware(t, 16, 8)

	res := New().Probe(context.Background(), fw.addr())
	if !res.Reachable || res.Protocol != "reffw" {
		t.Fatalf("probe = %+v, want reachable reffw", res)
	}

	dev := connectDevice(t, fw.addr())
	info := dev.Info(context.Background())
	if info.ScreenWidth != 16 || info.ScreenHeight != 8 {
		t.Fatalf("screen = %dx%d, want 16x8", info.ScreenWidth, info.ScreenHeight)
	}
	if info.Model != "MockRef" || info.Driver != DriverName {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestProbeRejectsForeignServer(t *testing.T) {
	// A server that does not implement /ledlink/info returns 404.
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	res := New().Probe(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if res.Reachable {
		t.Fatal("a foreign HTTP server must not be identified as reference firmware")
	}
}

func TestSendTextStreamsDDP(t *testing.T) {
	fw := startMockFirmware(t, 16, 8)
	dev := connectDevice(t, fw.addr())

	if err := dev.SendText(context.Background(), core.TextSpec{Text: "H", Color: "#ff0000"}); err != nil {
		t.Fatal(err)
	}

	select {
	case frame := <-fw.frames:
		if len(frame) != 16*8*3 {
			t.Fatalf("frame = %d bytes, want %d", len(frame), 16*8*3)
		}
		lit := false
		for _, b := range frame {
			if b != 0 {
				lit = true
				break
			}
		}
		if !lit {
			t.Fatal("frame is all black; text did not render")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame received by the mock firmware")
	}
}

func TestControlEndpoints(t *testing.T) {
	fw := startMockFirmware(t, 8, 4)
	dev := connectDevice(t, fw.addr())
	ctx := context.Background()

	if err := dev.SetBrightness(ctx, 42); err != nil {
		t.Fatal(err)
	}
	fw.mu.Lock()
	gotBrightness := fw.brightness
	fw.mu.Unlock()
	if gotBrightness != 42 {
		t.Fatalf("firmware brightness = %d, want 42", gotBrightness)
	}

	if err := dev.ScreenPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	fw.mu.Lock()
	gotPower := fw.powerOn
	fw.mu.Unlock()
	if gotPower {
		t.Fatal("firmware power should be off")
	}
}

func TestValidation(t *testing.T) {
	fw := startMockFirmware(t, 8, 4)
	dev := connectDevice(t, fw.addr())
	ctx := context.Background()

	if err := dev.SendText(ctx, core.TextSpec{Text: "  "}); err == nil {
		t.Fatal("empty text should fail")
	}
	if err := dev.SetBrightness(ctx, 0); err == nil {
		t.Fatal("brightness 0 should fail")
	}
	if _, err := dev.UploadMedia(ctx, []string{"/tmp/x.jpg"}); err == nil {
		t.Fatal("reference firmware has no storage; upload should fail")
	}
}
