package httpapi

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// syncAddressPerMinute is only an abuse ceiling per source address for all sync traffic.
// One Silo server sends every connected profile's traffic from one address, so the real
// budget is per sync credential (Server.syncPerMinute). Silo syncs its connections one
// at a time, so its address stays far below this.
const syncAddressPerMinute = 6000

// syncFailuresPerMinute bounds requests with keys Pastime never issued per source
// address. An address past it is refused before any key lookup until the window ends.
// Known keys that were revoked or expired do not count.
const syncFailuresPerMinute = 120

// syncRoutes are the routes a Silo sync credential (ptbs_) may call.
var syncRoutes = map[string]bool{
	"/api/v1/integrations/bloem/credentials:inspect": true,
	"/api/v1/integrations/bloem/events:apply":        true,
	"/api/v1/integrations/bloem/state/events:apply":  true,
	"/api/v1/integrations/bloem/state:read":          true,
}

// faultWindow is how long refusals are counted before one log line per route and code.
const faultWindow = time.Minute

type faultKey struct{ route, code string }

// faultCounter counts refused sync requests by route and problem code. It never sees
// credentials or identifiers. Counts are logged once per window after the first refusal.
type faultCounter struct {
	logger *slog.Logger
	mu     sync.Mutex
	counts map[faultKey]int
	timer  *time.Timer
}

func (c *faultCounter) add(route, code string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[faultKey]int{}
	}
	c.counts[faultKey{route, code}]++
	if c.timer == nil {
		c.timer = time.AfterFunc(faultWindow, c.flush)
	}
}

func (c *faultCounter) flush() {
	c.mu.Lock()
	counts := c.counts
	c.counts, c.timer = nil, nil
	c.mu.Unlock()
	keys := make([]faultKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].route < keys[j].route || keys[i].route == keys[j].route && keys[i].code < keys[j].code
	})
	logger := c.logger
	if logger == nil {
		logger = slog.Default()
	}
	for _, k := range keys {
		logger.Info("silo sync requests refused", "route", k.route, "code", k.code, "count", counts[k], "window_seconds", int(faultWindow.Seconds()))
	}
}

// countSyncFault records a refused sync request: a 4xx answer on a sync route.
func (s *Server) countSyncFault(rctx *chi.Context, code string) {
	if rctx == nil {
		return
	}
	if route := rctx.RoutePattern(); syncRoutes[route] {
		s.syncFaults.add(route, code)
	}
}
