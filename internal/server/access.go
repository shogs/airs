package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/state"
)

// Allowed decides whether a request may see item.
func Allowed(item state.Item, cfg config.Config, trusted, loopback bool) bool {
	return loopback || trusted || cfg.AnyNetwork || item.AnyNetwork
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

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
