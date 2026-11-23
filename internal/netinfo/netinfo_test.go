package netinfo

import (
	"os"
	"testing"
)

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseRouteGet(t *testing.T) {
	gw, iface, err := parseRouteGet(fixture(t, "route_get.txt"))
	if err != nil || gw != "192.168.1.1" || iface != "en0" {
		t.Fatalf("got %q %q %v", gw, iface, err)
	}
	if _, _, err := parseRouteGet(fixture(t, "route_none.txt")); err == nil {
		t.Fatal("want error for no route")
	}
}

func TestFormatMAC(t *testing.T) {
	if got := formatMAC([]byte{0xa4, 0x2b, 0xb0, 0x02, 0x34, 0x56}); got != "a4:2b:b0:02:34:56" {
		t.Fatal(got)
	}
	if got := formatMAC(nil); got != "" {
		t.Fatal(got)
	}
}

func TestFingerprint(t *testing.T) {
	i := Info{GatewayIP: "192.168.1.1", GatewayMAC: "A4:2B:B0:02:34:56"}
	if i.Fingerprint() != "192.168.1.1|a4:2b:b0:02:34:56" {
		t.Fatal(i.Fingerprint())
	}
}

func TestFake(t *testing.T) {
	var s Source = Fake{Info: Info{LocalIP: "10.0.0.2"}}
	if i, err := s.Current(); err != nil || i.LocalIP != "10.0.0.2" {
		t.Fatal(i, err)
	}
}
