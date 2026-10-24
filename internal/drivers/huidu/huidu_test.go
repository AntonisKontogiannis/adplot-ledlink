package huidu

import (
	"testing"

	huidu "github.com/alparslanahmed/huidu-led"
)

func TestSplitAddr(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"192.168.1.10", "192.168.1.10", 10001},
		{"192.168.1.10:8787", "192.168.1.10", 8787},
		{"screen.local:10001", "screen.local", 10001},
	}
	for _, c := range cases {
		host, port := splitAddr(c.in)
		if host != c.host || port != c.port {
			t.Errorf("splitAddr(%q) = %s:%d, want %s:%d", c.in, host, port, c.host, c.port)
		}
	}
}

func TestMapEffect(t *testing.T) {
	if e, err := mapEffect(""); err != nil || e != huidu.EffectImmediate {
		t.Fatalf("empty effect should map to immediate, got %v, %v", e, err)
	}
	if e, err := mapEffect("left-scroll-loop"); err != nil || e != huidu.EffectLeftScrollLoop {
		t.Fatalf("left-scroll-loop mapping wrong: %v, %v", e, err)
	}
	if _, err := mapEffect("does-not-exist"); err == nil {
		t.Fatal("unknown effect should fail")
	}
}

func TestMapAlign(t *testing.T) {
	if a, err := mapAlign(""); err != nil || a != huidu.HAlignCenter {
		t.Fatalf("default align should be center: %v %v", a, err)
	}
	if a, err := mapAlign("right"); err != nil || a != huidu.HAlignRight {
		t.Fatalf("right align mapping wrong: %v %v", a, err)
	}
	if _, err := mapAlign("sideways"); err == nil {
		t.Fatal("unknown align should fail")
	}
}
