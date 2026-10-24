package ddp

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/ddpwire"
)

// startReceiver listens on an ephemeral UDP port and forwards every received
// datagram to a channel, simulating a DDP pixel receiver.
func startReceiver(t *testing.T) (string, chan []byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })

	ch := make(chan []byte, 64)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pkt := make([]byte, n)
			copy(pkt, buf[:n])
			select {
			case ch <- pkt:
			default:
			}
		}
	}()
	return pc.LocalAddr().String(), ch
}

func recv(t *testing.T, ch chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case pkt := <-ch:
		return pkt
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a DDP packet")
		return nil
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

func TestParseAddr(t *testing.T) {
	cases := []struct {
		in      string
		host    string
		port    int
		w, h    int
		wantErr bool
	}{
		{"192.168.1.50", "192.168.1.50", 4048, 64, 32, false},
		{"192.168.1.50:5000", "192.168.1.50", 5000, 64, 32, false},
		{"192.168.1.50?w=128&h=64", "192.168.1.50", 4048, 128, 64, false},
		{"192.168.1.50:5000?w=16&h=8", "192.168.1.50", 5000, 16, 8, false},
		{"?w=64", "", 0, 0, 0, true},
		{"host?w=0", "", 0, 0, 0, true},
		{"host?h=abc", "", 0, 0, 0, true},
	}
	for _, c := range cases {
		host, port, w, h, err := parseAddr(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseAddr(%q): want error, got %s:%d %dx%d", c.in, host, port, w, h)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddr(%q): %v", c.in, err)
			continue
		}
		if host != c.host || port != c.port || w != c.w || h != c.h {
			t.Errorf("parseAddr(%q) = %s:%d %dx%d, want %s:%d %dx%d",
				c.in, host, port, w, h, c.host, c.port, c.w, c.h)
		}
	}
}

func TestSendTextPushesFrame(t *testing.T) {
	addr, ch := startReceiver(t)
	dev := connectDevice(t, addr+"?w=16&h=8")

	if err := dev.SendText(context.Background(), core.TextSpec{Text: "A", Color: "#ff0000"}); err != nil {
		t.Fatal(err)
	}
	pkt := recv(t, ch, time.Second)

	wantLen := ddpwire.HeaderLen + 16*8*3
	if len(pkt) != wantLen {
		t.Fatalf("packet length = %d, want %d", len(pkt), wantLen)
	}
	if pkt[0] != ddpwire.FlagPushV1 || pkt[2] != ddpwire.TypeRGB24 || pkt[3] != 0 {
		t.Fatalf("unexpected header: % x", pkt[:ddpwire.HeaderLen])
	}
	if off := binary.BigEndian.Uint32(pkt[4:8]); off != 0 {
		t.Fatalf("offset = %d, want 0", off)
	}
	if l := binary.BigEndian.Uint16(pkt[8:10]); int(l) != wantLen-ddpwire.HeaderLen {
		t.Fatalf("payload length = %d, want %d", l, wantLen-ddpwire.HeaderLen)
	}

	lit := false
	for i := ddpwire.HeaderLen; i+3 <= len(pkt); i += 3 {
		if pkt[i] != 0 || pkt[i+1] != 0 || pkt[i+2] != 0 {
			lit = true
			break
		}
	}
	if !lit {
		t.Fatal("frame is all black; text did not render")
	}
}

func TestPowerOffSendsBlackThenSkips(t *testing.T) {
	addr, ch := startReceiver(t)
	dev := connectDevice(t, addr+"?w=8&h=4")
	ctx := context.Background()

	if err := dev.SendText(ctx, core.TextSpec{Text: "X"}); err != nil {
		t.Fatal(err)
	}
	recv(t, ch, time.Second) // initial frame

	if err := dev.ScreenPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	pkt := recv(t, ch, time.Second)
	for i := ddpwire.HeaderLen; i < len(pkt); i++ {
		if pkt[i] != 0 {
			t.Fatal("power-off frame should be black")
		}
	}

	// While off, new content is stored but not transmitted.
	if err := dev.SendText(ctx, core.TextSpec{Text: "Y"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Fatal("no packets should be sent while the screen is off")
	case <-time.After(150 * time.Millisecond):
	}

	// Powering back on replays the last content.
	if err := dev.ScreenPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	recv(t, ch, time.Second)
}

func TestValidation(t *testing.T) {
	addr, _ := startReceiver(t)
	dev := connectDevice(t, addr+"?w=8&h=4")
	ctx := context.Background()

	if err := dev.SendText(ctx, core.TextSpec{Text: "  "}); err == nil {
		t.Fatal("empty text should fail")
	}
	if err := dev.SetBrightness(ctx, 0); err == nil {
		t.Fatal("brightness 0 should fail")
	}
	if err := dev.SetBrightness(ctx, 200); err == nil {
		t.Fatal("brightness 200 should fail")
	}
	if _, err := dev.UploadMedia(ctx, []string{"/tmp/x.jpg"}); err == nil {
		t.Fatal("ddp has no storage; upload should fail")
	}
}
