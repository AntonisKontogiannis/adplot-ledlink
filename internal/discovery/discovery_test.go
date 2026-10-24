package discovery

import (
	"reflect"
	"testing"
)

func TestExpandCIDR(t *testing.T) {
	hosts, err := Expand("192.168.7.0/29", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.168.7.1", "192.168.7.2", "192.168.7.3", "192.168.7.4", "192.168.7.5", "192.168.7.6"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("got %v, want %v", hosts, want)
	}
}

func TestExpandListDedup(t *testing.T) {
	hosts, err := Expand("screen1, 10.0.0.5, screen1", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"screen1", "10.0.0.5"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("got %v, want %v", hosts, want)
	}
}

func TestExpandInvalid(t *testing.T) {
	if _, err := Expand("10.0.0.0/99", 0); err == nil {
		t.Fatal("invalid CIDR should fail")
	}
	if _, err := Expand("fd00::/64", 0); err == nil {
		t.Fatal("IPv6 CIDR should fail")
	}
}

func TestExpandCap(t *testing.T) {
	hosts, err := Expand("10.0.0.0/16", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 10 {
		t.Fatalf("expected 10 hosts, got %d", len(hosts))
	}
}
