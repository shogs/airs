// Package netinfo identifies the current network without Location permission.
package netinfo

import (
	"errors"
	"fmt"
	"strings"
)

// Info describes the current network.
type Info struct {
	GatewayIP  string
	GatewayMAC string
	Interface  string
	LocalIP    string
}

// Fingerprint identifies a network by its gateway IP and MAC address.
func (i Info) Fingerprint() string {
	return i.GatewayIP + "|" + strings.ToLower(i.GatewayMAC)
}

// Source reports the current network.
type Source interface {
	Current() (Info, error)
}

// Fake is a fixed Source for tests.
type Fake struct {
	Info Info
	Err  error
}

func (f Fake) Current() (Info, error) { return f.Info, f.Err }

func parseRouteGet(out string) (gateway, iface string, err error) {
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch k {
		case "gateway":
			gateway = strings.TrimSpace(v)
		case "interface":
			iface = strings.TrimSpace(v)
		}
	}
	if gateway == "" || iface == "" {
		return "", "", errors.New("no default route")
	}
	return gateway, iface, nil
}

func formatMAC(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}
