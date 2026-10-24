# LED Link reference firmware — specification v0.1

A small contract that makes bare HUB75 LED panels **plug-and-play for adplot**:
any ESP32 / Raspberry Pi class controller that implements this spec is
recognized and driven by LED Link with no vendor SDK in the loop.

```
adplot ──REST──▶ LED Link ──┬─ HTTP /ledlink/*   (identity + control)
                            └─ UDP DDP :4048     (pixel streaming)
                                      │
                            reference firmware (ESP32 / Pi)
                                      │
                                 HUB75 panel
```

## MUST — identity: `GET /ledlink/info`

Respond `200` with JSON:

```json
{
  "name": "shop-window-panel",
  "model": "esp32-ref",
  "firmware": "0.1.0",
  "width": 64,
  "height": 32,
  "ddp_port": 4048
}
```

`width`/`height` are the total panel size in pixels. `ddp_port` may be omitted
when the DDP receiver listens on 4048. This endpoint is what `ledlink identify`
uses to recognize the device — it must answer within ~1 second.

## MUST — pixel streaming: DDP receiver

- Listen for UDP (default port 4048, or `ddp_port` from info).
- Accept **DDP v1** packets: 10-byte header
  `[flags 0x41 (v1 + push)] [sequence] [type 0x0B = RGB24] [destination 0]`
  followed by `uint32` big-endian **byte offset** and `uint16` big-endian
  **payload length** (≤ 1440 bytes), then RGB pixel bytes.
- Reassemble fragments by offset into a `width*height*3` framebuffer
  (row-major, RGB). A packet ending at the last byte completes a frame.
- Ignore out-of-range offsets/lengths; dropping packets is safe — senders
  refresh continuously.

## MUST — state: brightness and power

- `POST /ledlink/brightness` with `{"percent": 1..100}` — apply to output;
  respond `2xx`.
- `POST /ledlink/power` with `{"on": true|false}` — blank/restore output;
  respond `2xx`.

**SHOULD** persist both across reboots.

## SHOULD — discovery: mDNS

Advertise `_ledlink._tcp.local` with TXT records `model`, `width`, `height`.
(Not required today: LED Link finds panels by scanning the local subnet.)

## MAY — extras

- `GET /` minimal status page (name, size, brightness).
- OTA firmware update endpoint.
- Firmware-side text rendering for offline fallback.
- `POST /ledlink/test` self-test pattern (color bars).

## Conformance checklist

1. `ledlink identify <ip>` reports a match with driver `reffw`.
2. Connect through the API; `GET /api/v1/devices/<id>` shows the right size.
3. Send text: `POST .../text` — the rendered pixels appear.
4. `POST .../brightness` visibly changes output.
5. `POST .../power {"on":false}` blanks the panel; `true` restores it.

`ledlink scan --targets local` finds the panel automatically.

## Reference implementations

### Raspberry Pi / Linux SBC — shipped

`cmd/ledlink-refd` is a complete implementation of this spec:

```bash
go build -o ledlink-refd ./cmd/ledlink-refd
./ledlink-refd -width 64 -height 32 -http :8080 -ddp :4048
```

Plug real hardware into the frame sink (`panel.render` in `main.go`), for
example via `rpi-rgb-led-matrix` bindings; the shipped version validates the
stream and logs statistics.

### ESP32 — guidance

Combine existing ecosystems instead of writing from scratch:

- Panel driver: `ESP32-HUB75-MatrixPanel-I2S-DMA` (great refresh rates, BCM).
- DDP receive: small receiver examples exist for ESP32 (`esp_ddp`, WLED's DDP
  support) — or implement the packet rules above directly, they fit in ~100
  lines.
- HTTP + WiFi: `ESPAsyncWebServer`, station mode, plus mDNS
  (`ESPmDNS` with `_ledlink._tcp`).
- Memory: a full RGB framebuffer is 3 bytes/pixel (64×32 → 6 KB, fine;
  256×128 → 96 KB, use PSRAM). For very large panels, write received pixels
  row-by-row straight into the panel scan buffer instead of a full copy.

## Security

v0.1 is LAN-only and unauthenticated (same trust model as vendor LED tools).
Do not port-forward the HTTP endpoint to the internet.

## Changelog

- v0.1 (2026-10) — initial spec; implemented by `cmd/ledlink-refd` and
  `internal/drivers/reffw`.
