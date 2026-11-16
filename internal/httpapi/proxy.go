package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Trust forwarding headers only from explicitly configured peers. Walk the chain
// from the socket toward the client, stopping at the first untrusted hop.
func proxyRemoteAddr(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return r.RemoteAddr
	}
	contains := func(ip netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	if !contains(peer) {
		return r.RemoteAddr
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(hops) > 32 {
		return r.RemoteAddr
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return r.RemoteAddr
		}
		if !contains(ip) || i == 0 {
			return net.JoinHostPort(ip.Unmap().String(), "0")
		}
	}
	return r.RemoteAddr
}
