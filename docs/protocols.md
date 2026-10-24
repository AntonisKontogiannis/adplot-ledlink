# LED controller protocols — research notes

Background research collected for LED Link (October 2026). Sources are public
documentation, official SDKs and community reverse engineering; verify details
against your own hardware before relying on them.

## Huidu (HDPlayer / HD2020 / LedArt)

Full-color async controllers (HD-WF2, A/C/D series) speak the **Huidu SDK 2.0
binary TCP protocol**:

- TCP on port **10001**; frame format `u16 length LE | u16 command LE | payload`.
- Handshake: version (0x2001/0x2002) → SDK XML negotiation (0x2003/0x2004, device
  assigns a session GUID) → device info request.
- File transfer: 0x8001–0x8006, chunks ≤ 8000 bytes, MD5 verification, resume.
- Programs are XML (`.boo`): `Screen → Program → Area → Item` with text, image,
  video, clock items and 30 transition effects.
- UDP device discovery on port **9527**.
- Defaults from public guides: WiFi AP password `88888888`; admin/hardware
  password `168` or `888`.
- Cloud platforms (XiaoHui / OMS, `clouds.huidu.cn`) exist for remote fleets.

References:

- <https://github.com/alparslanahmed/huidu-led> — Go implementation (MIT),
  reverse-engineered from the official C# SDK 2.0.10; wraps in `drivers/huidu`.
- <https://github.com/Bradyok/Huidu> — HDPlayer 7.11.8 decompilation reference
  (command tables, `.boo` XML spec, UDP discovery layout).

## Onbon (BX-Y1A, BX-5K/6K, 6Q/6E)

Onbon publishes **official SDKs** on GitHub for its controller families:

- <https://github.com/onbonlab/BX-Y> — SDK for the BX-Y family (C#, C++, Delphi
  samples); the BX-Y1A video controller belongs to this family.
- <https://github.com/onbonlab/bx.k.protocol>, `bx.k.doc`, `bx.dual.doc` —
  protocol documents for BX-5K/6K and G5/G6 dual-color cards.
- <https://github.com/strongercjd/Onbon-Protocol-Analysis> — third-party tool
  that parses captured Onbon open-protocol traffic (5K/6K font cards, 6Q/6E
  dynamic areas) for reference.

Onbon async cards are driven by LedshowYQ on Windows; the Phase 2 adapter will
port the BX-Y SDK protocol to Go.

## Colorlight (5A-75x) — implemented (streaming class)

Receiver cards driven by raw Ethernet frames rather than file-based programs.
Protocol (firmware 13.x, from public reverse-engineering notes):

- Fixed MACs, Gigabit link only, BGR pixel order.
- EtherTypes: `0x0AFF` brightness/frame-start, `0x5500|rowMSB` pixel rows
  (7-byte header, ≤497 pixels per packet), `0x0101` buffer-swap (sent ×2).
- Firmware 13.x double-buffers: the flicker-free sequence is
  2×0x0AFF → rows → 2×0x0101, then brightness + rows again to fill the second
  buffer without swapping.
- The discover packet (`0x0700`) leaves cards unresponsive until a power
  cycle — LED Link never sends it.
- References: <https://github.com/menull/colorlight-5a75b-protocol> (public
  domain notes); Falcon Player `ColorLight-5a-75.cpp` (frame sequence).
- LED Link implementation: `internal/drivers/colorlight` (Linux AF_PACKET,
  needs root or CAP_NET_RAW).

## Reference firmware — bare panels, plug-and-play

For HUB75 modules with no commercial controller, LED Link defines a minimal
firmware contract (HTTP info/control + DDP streaming):

- Spec: [reference-firmware.md](reference-firmware.md).
- Reference daemon: `cmd/ledlink-refd` (Pi / Linux; plug a panel library into
  the frame sink).
- Driver: `internal/drivers/reffw` — `ledlink identify` recognizes reference
  firmware devices over HTTP, so bare panels are auto-detected and driven.

## DDP — the open, vendor-neutral option (LED Link `ddp` driver)

The Distributed Display Protocol is the closest thing to a universal pixel
protocol and is implemented by Falcon Player (FPP), WLED, ESPixelStick and
many DIY ESP32 / Raspberry Pi LED controllers:

- UDP, default port **4048**.
- 10-byte header: flags (v1 + push = `0x41`), sequence byte, data type
  (`0x0B` = RGB24), destination id, **32-bit big-endian offset**, **16-bit
  big-endian payload length**; then raw pixel data (≤ 1440 bytes per datagram
  to stay under the MTU).
- No handshake and no discovery: the pixel count is configured on the sender
  side (`host?w=64&h=32` in the LED Link address).
- LED Link implementation: `internal/drivers/ddp` with text rasterization in
  `internal/render`. Validation: unit tests run against a live UDP receiver
  implementing the spec above; real-hardware validation pending.

Why it matters: it gives adplot a fully open path to any HUB75 panel — the
basis for the "own reference controller" roadmap item, with no vendor SDK in
the loop.

## Linsn / NovaStar — mostly closed

No public protocol implementations found; Linsn remote-control automation exists
(<https://github.com/ethereali/linsn-ledstudio-remote-control>) but drives the
Windows LEDStudio app rather than speaking the protocol. Cloud APIs are the
pragmatic fallback for these vendors.

## Design implications

- There is **no universal protocol**; LED Link uses one adapter per vendor
  family and a shared `Screen → Program → Area → Item` style content model that
  maps naturally onto both Huidu and Onbon program structures.
- Integration classes differ: async *program-based* controllers (Huidu, Onbon)
  accept playlists and media files; *streaming* controllers (Colorlight,
  NovaStar, Linsn) need a frame-streaming adapter instead.
- Discovery strategies per family: subnet probe of the protocol port (works
  today), UDP broadcast, and vendor cloud lookup.

## Generic layer (added by LED Link)

Because no universal protocol exists, genericity lives in LED Link itself:
every adapter implements `Probe`, so `ledlink identify <address>` and
`driver: "auto"` can fingerprint an unknown controller by testing all
registered families and then connect with the matching one. Supporting a new
vendor family therefore means writing one adapter — the platform, the REST API
and adplot integration stay untouched.
