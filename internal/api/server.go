// Package api exposes the LED Link REST API used by adplot and other clients.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/discovery"
	"adplot/ledlink/internal/webui"
)

// Server serves the LED Link REST API on top of a core.Manager.
type Server struct {
	Mgr     *core.Manager
	Version string
	Log     *log.Logger
}

// New returns a Server.
func New(mgr *core.Manager, version string, logger *log.Logger) *Server {
	return &Server{Mgr: mgr, Version: version, Log: logger}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/drivers", s.handleDrivers)
	mux.HandleFunc("POST /api/v1/scan", s.handleScan)
	mux.HandleFunc("POST /api/v1/identify", s.handleIdentify)

	mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /api/v1/devices", s.handleConnectDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.handleDisconnectDevice)
	mux.HandleFunc("POST /api/v1/devices/{id}/text", s.handleSendText)
	mux.HandleFunc("POST /api/v1/devices/{id}/brightness", s.handleBrightness)
	mux.HandleFunc("POST /api/v1/devices/{id}/power", s.handlePower)
	mux.HandleFunc("POST /api/v1/devices/{id}/media", s.handleUploadMedia)

	// The browser control interface is embedded in the binary and served at /.
	mux.Handle("GET /", webui.Handler())

	return withCORS(withLogging(s.Log, mux))
}

// --- handlers ---------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}

func (s *Server) handleDrivers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"drivers": s.Mgr.Registry.Names()})
}

type scanRequest struct {
	Driver    string `json:"driver"`     // adapter name; "" or "auto" probes every adapter
	Targets   string `json:"targets"`    // "local" (default), host list, or CIDR
	TimeoutMS int    `json:"timeout_ms"` // per-probe timeout, default 800
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if !decode(w, r, &req) {
		return
	}
	auto := req.Driver == "" || req.Driver == "auto" || req.Driver == "all"

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}

	targets, err := discovery.Expand(req.Targets, discovery.DefaultMaxHosts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no scan targets found"))
		return
	}

	var results []core.ScanResult
	if auto {
		results, err = s.Mgr.ScanAll(r.Context(), targets, timeout)
	} else {
		results, err = s.Mgr.Scan(r.Context(), req.Driver, targets, timeout)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"probed":  len(targets),
		"found":   len(results),
		"devices": results,
	})
}

type identifyRequest struct {
	Address   string `json:"address"`
	TimeoutMS int    `json:"timeout_ms"` // per-driver probe timeout, default 2000
}

// handleIdentify probes the address with every registered driver and reports
// which families answered — the generic "what is this device?" endpoint.
func (s *Server) handleIdentify(w http.ResponseWriter, r *http.Request) {
	var req identifyRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Address == "" {
		writeError(w, http.StatusBadRequest, errors.New("address is required"))
		return
	}

	results := s.Mgr.Identify(r.Context(), req.Address, time.Duration(req.TimeoutMS)*time.Millisecond)
	matches := make([]core.IdentifyResult, 0, len(results))
	for _, res := range results {
		if res.Reachable {
			matches = append(matches, res)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"address": req.Address,
		"probed":  len(results),
		"matches": matches,
	})
}

type connectRequest struct {
	// Driver may be an adapter name, or "" / "auto" for auto-detection.
	Driver  string `json:"driver"`
	Address string `json:"address"`
}

func (s *Server) handleConnectDevice(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Address == "" {
		writeError(w, http.StatusBadRequest, errors.New("address is required"))
		return
	}

	var info core.DeviceInfo
	var err error
	if req.Driver == "" || req.Driver == "auto" {
		info, err = s.Mgr.ConnectAuto(r.Context(), req.Address)
	} else {
		info, err = s.Mgr.Connect(r.Context(), req.Driver, req.Address)
	}
	if err != nil {
		s.writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.Mgr.List()
	if devices == nil {
		devices = []core.DeviceInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, ok := s.Mgr.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("device %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, dev.Info(r.Context()))
}

func (s *Server) handleDisconnectDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Mgr.Disconnect(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected", "id": id})
}

func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.device(w, r)
	if !ok {
		return
	}
	var spec core.TextSpec
	if !decode(w, r, &spec) {
		return
	}
	if err := dev.SendText(r.Context(), spec); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

type brightnessRequest struct {
	Percent int `json:"percent"`
}

func (s *Server) handleBrightness(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.device(w, r)
	if !ok {
		return
	}
	var req brightnessRequest
	if !decode(w, r, &req) {
		return
	}
	if err := dev.SetBrightness(r.Context(), req.Percent); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "percent": req.Percent})
}

type powerRequest struct {
	On bool `json:"on"`
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.device(w, r)
	if !ok {
		return
	}
	var req powerRequest
	if !decode(w, r, &req) {
		return
	}
	if err := dev.ScreenPower(r.Context(), req.On); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "on": req.On})
}

type mediaRequest struct {
	Files []string `json:"files"`
}

func (s *Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.device(w, r)
	if !ok {
		return
	}
	var req mediaRequest
	if !decode(w, r, &req) {
		return
	}
	names, err := dev.UploadMedia(r.Context(), req.Files)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "uploaded", "files": names})
}

// --- helpers ----------------------------------------------------------------

// device resolves the {id} path value to a connected device.
func (s *Server) device(w http.ResponseWriter, r *http.Request) (core.Device, bool) {
	id := r.PathValue("id")
	dev, ok := s.Mgr.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("device %q not found", id))
		return nil, false
	}
	return dev, true
}

// writeManagerError maps core sentinel errors to HTTP status codes.
func (s *Server) writeManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrAlreadyConnected):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, core.ErrUnknownDriver):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, core.ErrNoDriverMatch):
		writeError(w, http.StatusNotFound, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// decode reads a JSON body into dst; on failure it writes a 400 and reports
// false.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // response already started; nothing to do on error
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// withCORS allows adplot (running on any origin) to call the local API.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

func withLogging(logger *log.Logger, next http.Handler) http.Handler {
	if logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}
