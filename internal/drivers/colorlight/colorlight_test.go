package colorlight

import (
	"bytes"
	"context"
	"testing"
)

// fakeSender records every frame it is asked to send.
type fakeSender struct {
	frames [][]byte
}

func (f *fakeSender) Send(frame []byte) error {
	cp := make([]byte, len(frame))
	copy(cp, frame)
	f.frames = append(f.frames, cp)
	return nil
}

func (f *fakeSender) Close() error { return nil }

func newTestDevice(w, h int) (*device, *fakeSender) {
	sender := &fakeSender{}
	dev := &device{
		sender:     sender,
		id:         "test",
		addr:       "test0?w=x",
		iface:      "test0",
		w:          w,
		h:          h,
		brightness: 100,
		poweredOn:  true,
	}
	return dev, sender
}

func TestBrightnessAndSyncPackets(t *testing.T) {
	bri := brightnessPacket(100)
	if len(bri) != 14+brightnessPayloadLen {
		t.Fatalf("brightness packet length = %d, want %d", len(bri), 14+brightnessPayloadLen)
	}
	if !bytes.Equal(bri[0:6], []byte(dstMAC)) || !bytes.Equal(bri[6:12], []byte(srcMAC)) {
		t.Fatal("brightness packet has wrong MACs")
	}
	if bri[12] != 0x0A || bri[13] != 0xFF {
		t.Fatalf("brightness EtherType = %02x%02x, want 0aff", bri[12], bri[13])
	}
	if bri[14] != 0xFF || bri[15] != 0xFF || bri[16] != 0xFF || bri[17] != 0xFF {
		t.Fatalf("brightness payload = % x, want ffffffff...", bri[14:18])
	}

	half := brightnessPacket(50)
	if half[14] != 127 {
		t.Fatalf("50%% brightness level = %d, want 127", half[14])
	}

	sync := syncPacket()
	if len(sync) != 14+syncPayloadLen {
		t.Fatalf("sync packet length = %d, want %d", len(sync), 14+syncPayloadLen)
	}
	if sync[12] != 0x01 || sync[13] != 0x01 {
		t.Fatalf("sync EtherType = %02x%02x, want 0101", sync[12], sync[13])
	}
	for i := 14; i < len(sync); i++ {
		if sync[i] != 0 {
			t.Fatal("sync payload must be zero-filled")
		}
	}
}

func TestRowPacketsBGR(t *testing.T) {
	// 4x2 screen; first pixel RGB(0x11,0x22,0x33).
	rgb := make([]byte, 4*2*3)
	copy(rgb[0:3], []byte{0x11, 0x22, 0x33})

	rows := rowPackets(rgb, 4, 2)
	if len(rows) != 2 {
		t.Fatalf("row packets = %d, want 2", len(rows))
	}
	p := rows[0]
	if p[12] != 0x55 || p[13] != 0x00 {
		t.Fatalf("row EtherType = %02x%02x, want 5500", p[12], p[13])
	}
	// 7-byte pixel header: row LSB, offset MSB/LSB, count MSB/LSB, 0x08, 0x88.
	if p[14] != 0 || p[15] != 0 || p[16] != 0 || p[17] != 0 || p[18] != 4 {
		t.Fatalf("row header = % x, want row=0 off=0 count=4", p[14:19])
	}
	if p[19] != 0x08 || p[20] != 0x88 {
		t.Fatalf("row header constants = %02x %02x, want 08 88", p[19], p[20])
	}
	if p[21] != 0x33 || p[22] != 0x22 || p[23] != 0x11 {
		t.Fatalf("pixel bytes = % x, want 33 22 11 (BGR)", p[21:24])
	}
	if rows[1][14] != 1 {
		t.Fatalf("second row LSB = %d, want 1", rows[1][14])
	}
}

func TestWideRowSplits(t *testing.T) {
	rgb := make([]byte, 600*1*3)
	rows := rowPackets(rgb, 600, 1)
	if len(rows) != 2 {
		t.Fatalf("row packets = %d, want 2 (600 px split at 497)", len(rows))
	}
	first, second := rows[0], rows[1]
	if first[15] != 0x00 || first[16] != 0x00 || first[17] != 0x01 || first[18] != 0xF1 {
		t.Fatalf("first packet header = % x, want off=0 count=497", first[15:19])
	}
	if second[15] != 0x01 || second[16] != 0xF1 || second[17] != 0x00 || second[18] != 103 {
		t.Fatalf("second packet header = % x, want off=497 count=103", second[15:19])
	}
}

func TestSendFrameSequence(t *testing.T) {
	dev, sender := newTestDevice(2, 2)

	rgb := make([]byte, 2*2*3)
	rgb[0] = 0x11 // first pixel R
	if err := dev.sendFrameLocked(rgb); err != nil {
		t.Fatal(err)
	}

	// 2 bri + 2 rows + 2 sync + 2 bri + 2 rows = 10 packets.
	if len(sender.frames) != 10 {
		t.Fatalf("frames sent = %d, want 10", len(sender.frames))
	}
	types := []string{"bri", "bri", "row", "row", "sync", "sync", "bri", "bri", "row", "row"}
	for i, frame := range sender.frames {
		et := uint16(frame[12])<<8 | uint16(frame[13])
		var got string
		switch {
		case et == etherTypeBrightness:
			got = "bri"
		case et == etherTypeSync:
			got = "sync"
		case et&0xFF00 == etherTypePixelBase:
			got = "row"
		}
		if got != types[i] {
			t.Fatalf("frame %d type = %q (%04x), want %q", i, got, et, types[i])
		}
	}

	// Tiny rows are padded to the Ethernet minimum (14+7+6 = 27 -> 60).
	if len(sender.frames[2]) != minEthernetFrame {
		t.Fatalf("row frame length = %d, want %d (padded)", len(sender.frames[2]), minEthernetFrame)
	}
	// BGR conversion: R stays in the last byte of the pixel.
	if sender.frames[2][23] != 0x11 {
		t.Fatalf("first pixel R byte = %02x, want 11", sender.frames[2][23])
	}

	// Identical content is skipped.
	before := len(sender.frames)
	if err := dev.sendFrameLocked(rgb); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) != before {
		t.Fatal("identical content should not be re-sent")
	}
}

func TestBrightnessAndPowerBehavior(t *testing.T) {
	dev, sender := newTestDevice(2, 2)
	ctx := context.Background()

	rgb := make([]byte, 2*2*3)
	if err := dev.sendFrameLocked(rgb); err != nil {
		t.Fatal(err)
	}
	base := len(sender.frames)

	if err := dev.SetBrightness(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) <= base {
		t.Fatal("brightness change should re-transmit the frame")
	}
	if sender.frames[base][14] != 127 {
		t.Fatalf("resend brightness level = %d, want 127", sender.frames[base][14])
	}

	if err := dev.SetBrightness(ctx, 0); err == nil {
		t.Fatal("brightness 0 should fail")
	}

	if err := dev.ScreenPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	black := sender.frames[len(sender.frames)-4] // first row packet of the black frame
	for i := 21; i < 21+2*3; i++ {
		if black[i] != 0 {
			t.Fatal("power-off frame must be black")
		}
	}

	// While off, new content is stored but not transmitted.
	off := len(sender.frames)
	changed := make([]byte, 2*2*3)
	changed[0] = 0xFF
	if err := dev.sendFrameLocked(changed); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) != off {
		t.Fatal("no frames should be sent while powered off")
	}

	// Powering on replays the stored content.
	if err := dev.ScreenPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) <= off {
		t.Fatal("power on should replay the last frame")
	}
}

func TestParseAddr(t *testing.T) {
	cases := []struct {
		in      string
		iface   string
		w, h    int
		wantErr bool
	}{
		{"enp3s0", "enp3s0", 64, 32, false},
		{"eth0?w=320&h=256", "eth0", 320, 256, false},
		{"wlan0?h=64", "wlan0", 64, 64, false},
		{"", "", 0, 0, true},
		{"?w=64", "", 0, 0, true},
		{"eth0?w=0", "", 0, 0, true},
		{"eth0?h=abc", "", 0, 0, true},
	}
	for _, c := range cases {
		iface, w, h, err := parseAddr(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseAddr(%q): want error, got %s %dx%d", c.in, iface, w, h)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddr(%q): %v", c.in, err)
			continue
		}
		if iface != c.iface || w != c.w || h != c.h {
			t.Errorf("parseAddr(%q) = %s %dx%d, want %s %dx%d", c.in, iface, w, h, c.iface, c.w, c.h)
		}
	}
}
