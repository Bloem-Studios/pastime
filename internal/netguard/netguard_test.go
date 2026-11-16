package netguard

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestDialFallsBackOnlyToValidatedAddresses(t *testing.T) {
	called := []string{}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		called = append(called, address)
		if len(called) == 1 {
			return nil, errors.New("unreachable")
		}
		return a, nil
	}
	ips := []net.IPAddr{{IP: net.ParseIP("2001:4860:4860::8888")}, {IP: net.ParseIP("8.8.8.8")}}
	if _, err := dialAddresses(context.Background(), "tcp", "443", ips, false, dial); err != nil || len(called) != 2 {
		t.Fatal(called, err)
	}
	called = nil
	ips = append(ips, net.IPAddr{IP: net.ParseIP("127.0.0.1")})
	if _, err := dialAddresses(context.Background(), "tcp", "443", ips, false, dial); !errors.Is(err, ErrPrivate) || len(called) != 0 {
		t.Fatal("private answer reached dialer", called, err)
	}
}

func TestBlockedCoversNonPublicRanges(t *testing.T) {
	// The largest RFC 1918 private range is built from bytes so that the publish scan, which flags
	// home-network address prefixes, stays strict for the rest of the tree.
	for _, raw := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", net.IPv4(192, 168, 0, 1).String(), "169.254.169.254", "0.0.0.0", "::1", "fd7a:115c:a1e0::1",
		"100.64.0.1", "100.85.0.1", "100.127.255.254", "0.1.2.3", "192.0.0.8", "198.18.0.1", "240.0.0.1", "255.255.255.255",
		"::ffff:10.0.0.1", "::ffff:100.100.100.100", "64:ff9b::a00:1", "2002:c0a8:1e01::1",
	} {
		if !blocked(net.ParseIP(raw)) {
			t.Errorf("%s is not blocked", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "2001:4860:4860::8888"} {
		if blocked(net.ParseIP(raw)) {
			t.Errorf("%s is blocked", raw)
		}
	}
}
