package mock

import (
	"context"
	"testing"

	"adplot/ledlink/internal/core"
)

func TestProbe(t *testing.T) {
	d := New()
	if res := d.Probe(context.Background(), "mock-1"); !res.Reachable {
		t.Fatalf("mock-1 should be reachable, got %+v", res)
	}
	if res := d.Probe(context.Background(), "192.168.1.10"); res.Reachable {
		t.Fatal("a real host should not be reachable by the mock driver")
	}
}

func TestConnectAndControl(t *testing.T) {
	d := New()
	ctx := context.Background()

	if _, err := d.Connect(ctx, "192.168.1.10"); err == nil {
		t.Fatal("connecting a non-mock address should fail")
	}

	dev, err := d.Connect(ctx, "mock-2")
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	info := dev.Info(ctx)
	if info.ScreenWidth != 192 || info.ScreenHeight != 96 {
		t.Fatalf("mock-2 should simulate a 192x96 screen, got %dx%d", info.ScreenWidth, info.ScreenHeight)
	}

	if err := dev.SendText(ctx, core.TextSpec{Text: "hello adplot"}); err != nil {
		t.Fatal(err)
	}
	if err := dev.SendText(ctx, core.TextSpec{}); err == nil {
		t.Fatal("empty text should fail")
	}
	if err := dev.SetBrightness(ctx, 0); err == nil {
		t.Fatal("brightness 0 should fail")
	}
	if err := dev.SetBrightness(ctx, 60); err != nil {
		t.Fatal(err)
	}
	if err := dev.ScreenPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	names, err := dev.UploadMedia(ctx, []string{"/tmp/promo.jpg"})
	if err != nil || len(names) != 1 || names[0] != "promo.jpg" {
		t.Fatalf("upload: names=%v err=%v", names, err)
	}
}
