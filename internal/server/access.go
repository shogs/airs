package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/state"
)

// Allowed decides whether a request may see item. trusted means the request
// arrived on the trusted network's address; local means it came from this Mac.
func Allowed(item state.Item, cfg config.Config, trusted, local bool) bool {
	return local || trusted || cfg.AnyNetwork || item.AnyNetwork
}

// CanWrite decides whether a request may add or edit notes. Unlike reading,
// a per-item --any never grants it: only this Mac, the trusted network, or a
// config that turns the network check off everywhere.
func CanWrite(cfg config.Config, trusted, local bool) bool {
	return local || trusted || cfg.AnyNetwork
}

// NetState is the current network and whether it is trusted.
type NetState struct {
	mu      sync.Mutex
	info    netinfo.Info
	trusted bool
}

func (n *NetState) Set(info netinfo.Info, trusted bool) {
	n.mu.Lock()
	n.info, n.trusted = info, trusted
	n.mu.Unlock()
}

func (n *NetState) Get() (netinfo.Info, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.info, n.trusted
}

// BaseURL is the LAN base URL for the current network.
func BaseURL(info netinfo.Info, port int) string {
	host := info.LocalIP
	if host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// ItemPath is the URL path of an item.
func ItemPath(it state.Item) string {
	p := "/" + url.PathEscape(it.Slug)
	if it.IsDir {
		p += "/"
	}
	return p
}

// ItemURL is the full URL of an item.
func ItemURL(base string, it state.Item) string { return base + ItemPath(it) }

func hostIP(hostport string) net.IP {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return net.ParseIP(host)
}

// isLocal reports whether the request came from this Mac: loopback, or one of
// its own addresses (opening the printed LAN link in a browser on the Mac).
func isLocal(r *http.Request) bool {
	ip := hostIP(r.RemoteAddr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// arrivedOn reports whether the request reached us on local IP ip, so trust in
// one network does not extend to other interfaces (VPN, Tailscale, a second port).
func arrivedOn(r *http.Request, ip string) bool {
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || ip == "" {
		return false
	}
	got := hostIP(la.String())
	return got != nil && got.Equal(net.ParseIP(ip))
}

// hostAllowed blocks DNS rebinding: browsers send the page's own domain in
// Host, so only IP literals, localhost and mDNS (.local) names are accepted.
func hostAllowed(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	return net.ParseIP(h) != nil || h == "localhost" || strings.HasSuffix(h, ".local")
}
