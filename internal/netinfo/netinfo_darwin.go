//go:build darwin

package netinfo

import (
	"errors"
	"net"
	"os/exec"
	"syscall"

	"golang.org/x/net/route"
)

type system struct{}

// System returns the macOS Source.
func System() Source { return system{} }

func (system) Current() (Info, error) {
	out, err := exec.Command("/sbin/route", "-n", "get", "default").CombinedOutput()
	if err != nil {
		return Info{}, errors.New("no default route")
	}
	gw, iface, err := parseRouteGet(string(out))
	if err != nil {
		return Info{}, err
	}
	info := Info{GatewayIP: gw, Interface: iface, LocalIP: localIPv4(iface)}
	mac, err := gatewayMAC(net.ParseIP(gw))
	if err != nil {
		return info, err
	}
	info.GatewayMAC = mac
	return info, nil
}

// gatewayMAC reads the ARP entry from the routing table via sysctl. Running
// /usr/sbin/arp, or this sysctl from a process spawned by a Go binary, sees an
// empty ARP table on recent macOS; processes started by launchd or a shell can read it.
func gatewayMAC(gw net.IP) (string, error) {
	gw4 := gw.To4()
	if gw4 == nil {
		return "", errors.New("gateway is not IPv4")
	}
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBType(syscall.NET_RT_FLAGS), syscall.RTF_LLINFO)
	if err != nil {
		return "", err
	}
	msgs, err := route.ParseRIB(route.RIBType(syscall.NET_RT_FLAGS), rib)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || len(rm.Addrs) <= syscall.RTAX_GATEWAY {
			continue
		}
		dst, ok := rm.Addrs[syscall.RTAX_DST].(*route.Inet4Addr)
		if !ok || !net.IP(dst.IP[:]).Equal(gw4) {
			continue
		}
		if ll, ok := rm.Addrs[syscall.RTAX_GATEWAY].(*route.LinkAddr); ok && len(ll.Addr) == 6 {
			return formatMAC(ll.Addr), nil
		}
	}
	return "", errors.New("gateway MAC unknown")
}

func localIPv4(iface string) string {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}
