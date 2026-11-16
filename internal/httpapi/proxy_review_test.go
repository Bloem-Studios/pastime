package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedProxyBoundary(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	for _, tc := range []struct{ peer, forward, want string }{{"127.0.0.1:123", "198.51.100.8", "198.51.100.8:0"}, {"198.51.100.2:123", "198.51.100.8", "198.51.100.2:123"}, {"127.0.0.1:123", "spoof, 198.51.100.8", "198.51.100.8:0"}, {"127.0.0.1:123", "bad", "127.0.0.1:123"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forward)
		if got := proxyRemoteAddr(r, trusted); got != tc.want {
			t.Errorf("got %s want %s", got, tc.want)
		}
	}
}
