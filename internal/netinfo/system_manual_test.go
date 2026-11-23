//go:build darwin && manual

package netinfo

import "testing"

// Run the compiled test binary directly (go test -c), not via go test: processes
// spawned by the go tool cannot read the ARP table.
func TestSystemManual(t *testing.T) {
	i, err := System().Current()
	if err != nil || i.GatewayMAC == "" || i.LocalIP == "" {
		t.Fatalf("%+v err=%v", i, err)
	}
	t.Logf("gateway=%s iface=%s local=%s mac-len=%d", i.GatewayIP, i.Interface, i.LocalIP, len(i.GatewayMAC))
}
