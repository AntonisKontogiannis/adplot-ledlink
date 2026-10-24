package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/drivers/mock"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	reg := core.NewRegistry()
	reg.Register(mock.New())
	ts := httptest.NewServer(New(core.NewManager(reg), "test", nil).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDeviceLifecycle(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}

	// Connect a mock device.
	resp = postJSON(t, ts.URL+"/api/v1/devices", map[string]string{"driver": "mock", "address": "mock-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("connect = %d, want 201", resp.StatusCode)
	}
	var info core.DeviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if info.ID != "mock-1" || info.Driver != "mock" {
		t.Fatalf("unexpected device info: %+v", info)
	}

	// Duplicate connect conflicts.
	resp = postJSON(t, ts.URL+"/api/v1/devices", map[string]string{"driver": "mock", "address": "mock-1"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate connect = %d, want 409", resp.StatusCode)
	}

	// Send text.
	resp = postJSON(t, ts.URL+"/api/v1/devices/mock-1/text", map[string]any{"text": "hello adplot", "effect": "left-scroll-loop"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send text = %d", resp.StatusCode)
	}

	// Out-of-range brightness is rejected by the driver (502).
	resp = postJSON(t, ts.URL+"/api/v1/devices/mock-1/brightness", map[string]int{"percent": 0})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("bad brightness = %d, want 502", resp.StatusCode)
	}

	// Valid brightness.
	resp = postJSON(t, ts.URL+"/api/v1/devices/mock-1/brightness", map[string]int{"percent": 55})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("brightness = %d", resp.StatusCode)
	}

	// Power off.
	resp = postJSON(t, ts.URL+"/api/v1/devices/mock-1/power", map[string]bool{"on": false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("power = %d", resp.StatusCode)
	}

	// List devices.
	resp, err = http.Get(ts.URL + "/api/v1/devices")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Devices []core.DeviceInfo `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(listing.Devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(listing.Devices))
	}

	// Disconnect.
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/devices/mock-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disconnect = %d", resp.StatusCode)
	}

	// Device is gone.
	resp, err = http.Get(ts.URL + "/api/v1/devices/mock-1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after disconnect = %d, want 404", resp.StatusCode)
	}
}

func TestScanFindsMockDevices(t *testing.T) {
	ts := newTestServer(t)

	resp := postJSON(t, ts.URL+"/api/v1/scan", map[string]any{"driver": "mock", "targets": "mock-1,mock-2"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan = %d", resp.StatusCode)
	}
	var out struct {
		Probed  int               `json:"probed"`
		Found   int               `json:"found"`
		Devices []core.ScanResult `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Found != 2 {
		t.Fatalf("want 2 devices found, got %d", out.Found)
	}
}

func TestIdentifyAndAutoConnect(t *testing.T) {
	ts := newTestServer(t)

	// Identify: only the mock driver is registered in the test server.
	resp := postJSON(t, ts.URL+"/api/v1/identify", map[string]string{"address": "mock-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("identify = %d", resp.StatusCode)
	}
	var idOut struct {
		Probed  int                   `json:"probed"`
		Matches []core.IdentifyResult `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idOut); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if idOut.Probed != 1 || len(idOut.Matches) != 1 || idOut.Matches[0].Driver != "mock" {
		t.Fatalf("unexpected identify result: %+v", idOut)
	}

	// Auto-connect: no driver in the request.
	resp = postJSON(t, ts.URL+"/api/v1/devices", map[string]string{"address": "mock-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("auto connect = %d, want 201", resp.StatusCode)
	}
	var info core.DeviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if info.Driver != "mock" || info.ID != "mock-1" {
		t.Fatalf("unexpected device info: %+v", info)
	}
}

func TestScanDefaultsToAllDrivers(t *testing.T) {
	ts := newTestServer(t)

	resp := postJSON(t, ts.URL+"/api/v1/scan", map[string]any{"targets": "mock-1,mock-2"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan = %d", resp.StatusCode)
	}
	var out struct {
		Found   int               `json:"found"`
		Devices []core.ScanResult `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Found != 2 {
		t.Fatalf("want 2 devices, got %d", out.Found)
	}
	for _, d := range out.Devices {
		if d.Driver != "mock" {
			t.Fatalf("unexpected driver %q", d.Driver)
		}
	}
}

func TestEmbeddedUI(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET / content-type = %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("adplot LED Link")) {
		t.Fatal("index page does not look like the LED Link UI")
	}

	for _, asset := range []string{"/style.css", "/app.js"} {
		resp, err := http.Get(ts.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", asset, resp.StatusCode)
		}
	}
}
