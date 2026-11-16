// Package netguard builds HTTP clients for addresses people type in, such as a
// notification webhook or a media server. Unless allowed, they refuse to connect to
// loopback, private, link-local and similar addresses after DNS resolution, so an
// address cannot be used to probe the server's own network.
package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

var ErrPrivate = errors.New("private network addresses are not allowed from this server")

// special lists ranges that are not public but that net.IP's predicates miss: shared
// address space (also used by Tailscale), "this network", IETF protocol assignments,
// benchmarking, reserved/broadcast, and the IPv6 forms that embed an IPv4 address
// (NAT64 and 6to4) and could otherwise reach a private IPv4 host.
var special = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

func blocked(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range special {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Client returns a client with the given overall timeout. Redirects are not followed.
func Client(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxIdleConns: 8, IdleConnTimeout: 60 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("no address for host")
			}
			return dialAddresses(ctx, network, port, ips, allowPrivate, dialer.DialContext)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Resolve once, validate the entire answer, then try only those literal addresses.
func dialAddresses(ctx context.Context, network, port string, ips []net.IPAddr, allowPrivate bool, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	for _, ip := range ips {
		if !allowPrivate && blocked(ip.IP) {
			return nil, ErrPrivate
		}
	}
	var failures []error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
		host := ip.IP.String()
		if ip.Zone != "" {
			host += "%" + ip.Zone
		}
		conn, err := dial(attempt, network, net.JoinHostPort(host, port))
		cancel()
		if err == nil {
			return conn, nil
		}
		failures = append(failures, err)
	}
	if len(failures) == 0 {
		return nil, errors.New("no address for host")
	}
	return nil, errors.Join(failures...)
}
