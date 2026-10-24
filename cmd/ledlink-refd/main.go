// Command ledlink-refd is the LED Link reference firmware: a tiny daemon for
// Raspberry Pi / Linux SBCs that makes a bare HUB75 panel plug-and-play for
// adplot. It serves the control API specified in docs/reference-firmware.md
// and receives pixels over DDP.
//
// The frame sink (panel.render) is the single hook where real hardware goes:
// call into a panel library such as rpi-rgb-led-matrix there. The shipped
// implementation validates the stream and logs statistics.
//
// Usage:
//
//	ledlink-refd -http :8080 -ddp :4048 -width 64 -height 32
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// panel holds the device state and the current framebuffer.
type panel struct {
	width, height int

	mu         sync.Mutex
	brightness int
	powerOn    bool
	frame      []byte
	frames     uint64
}

func main() {
	httpAddr := flag.String("http", ":8080", "HTTP control API listen address")
	ddpAddr := flag.String("ddp", ":4048", "DDP UDP listen address")
	width := flag.Int("width", 64, "panel width in pixels")
	height := flag.Int("height", 32, "panel height in pixels")
	name := flag.String("name", "ledlink-ref", "device name reported to LED Link")
	flag.Parse()

	if *width < 1 || *height < 1 || *width > 8192 || *height > 8192 {
		log.Fatal("width and height must be in 1..8192")
	}

	p := &panel{
		width:      *width,
		height:     *height,
		brightness: 100,
		powerOn:    true,
		frame:      make([]byte, *width**height*3),
	}

	udpConn, err := net.ListenPacket("udp", *ddpAddr)
	if err != nil {
		log.Fatalf("ddp listen %s: %v", *ddpAddr, err)
	}
	ddpPort := udpConn.LocalAddr().(*net.UDPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ledlink/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"name":     *name,
			"model":    "ledlink-refd",
			"firmware": "0.1.0",
			"width":    p.width,
			"height":   p.height,
			"ddp_port": ddpPort,
		})
	})
	mux.HandleFunc("POST /ledlink/brightness", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Percent int `json:"percent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Percent < 1 || req.Percent > 100 {
			http.Error(w, `{"error":"percent must be 1..100"}`, http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.brightness = req.Percent
		p.mu.Unlock()
		writeJSON(w, map[string]any{"status": "ok", "percent": req.Percent})
	})
	mux.HandleFunc("POST /ledlink/power", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.powerOn = req.On
		p.mu.Unlock()
		writeJSON(w, map[string]any{"status": "ok", "on": req.On})
	})

	go p.receiveLoop(udpConn)
	go p.statsLoop()

	log.Printf("ledlink-refd: %dx%d panel %q | DDP :%d | HTTP %s", *width, *height, *name, ddpPort, *httpAddr)
	log.Fatal(http.ListenAndServe(*httpAddr, mux))
}

// DDP v1 framing constants (see docs/reference-firmware.md).
const (
	ddpHeaderLen  = 10
	ddpMaxPayload = 1440
)

// receiveLoop reassembles DDP packets by offset and renders completed frames.
func (p *panel) receiveLoop(conn net.PacketConn) {
	buf := make([]byte, 65536)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < ddpHeaderLen || n > ddpHeaderLen+ddpMaxPayload {
			continue
		}
		off := int(binary.BigEndian.Uint32(buf[4:8]))
		length := int(binary.BigEndian.Uint16(buf[8:10]))
		if length != n-ddpHeaderLen || off+length > len(p.frame) {
			continue
		}

		p.mu.Lock()
		copy(p.frame[off:], buf[ddpHeaderLen:ddpHeaderLen+length])
		complete := off+length == len(p.frame)
		if complete {
			p.frames++
		}
		brightness, powerOn := p.brightness, p.powerOn
		p.mu.Unlock()

		if complete {
			p.render(brightness, powerOn)
		}
	}
}

// render hands the assembled frame to the panel output. This is the hook for
// real hardware: on a Raspberry Pi, call into rpi-rgb-led-matrix (or similar)
// here; the shipped implementation only validates the stream.
func (p *panel) render(brightness int, powerOn bool) {
	// Replace this body with the panel library call, for example:
	//   matrix.SetBrightness(brightness)
	//   matrix.SetPixels(p.frame)
	_, _ = brightness, powerOn
}

// statsLoop logs stream statistics every two seconds.
func (p *panel) statsLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p.mu.Lock()
		frames, brightness, on := p.frames, p.brightness, p.powerOn
		p.mu.Unlock()
		log.Printf("frames=%d brightness=%d%% power=%v", frames, brightness, on)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
