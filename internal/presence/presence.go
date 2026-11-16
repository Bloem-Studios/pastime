// Package presence records which server and worker processes are running, so
// administration can see every replica without reaching into each one.
package presence

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Interval is how often a process refreshes its row. Readers treat a row older than
// three intervals as a stopped process.
const Interval = 30 * time.Second

// Stats is the snapshot a process publishes with each heartbeat.
type Stats struct {
	Goroutines int                  `json:"goroutines"`
	HeapMB     float64              `json:"heap_mb"`
	GoVersion  string               `json:"go_version"`
	Pool       PoolStats            `json:"pool"`
	Providers  []provider.HostStats `json:"providers"`
	Concurrent int                  `json:"concurrency,omitempty"`
}

type PoolStats struct {
	Max      int32 `json:"max"`
	Total    int32 `json:"total"`
	Idle     int32 `json:"idle"`
	Acquired int32 `json:"acquired"`
}

// Node identifies this process.
type Node struct {
	Pool        *pgxpool.Pool
	Role        string // server or worker
	Version     string
	Concurrency int
	Providers   *provider.Client
	id          string
	started     time.Time
}

// Run publishes heartbeats until ctx ends, then removes the row. Failures are
// ignored: presence is advisory and must never take a process down.
func (n *Node) Run(ctx context.Context) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	n.id = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	n.started = time.Now()
	host, _ := os.Hostname()
	if len(host) > 255 {
		host = host[:255]
	}
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		n.beat(ctx, host)
		select {
		case <-ctx.Done():
			done, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = n.Pool.Exec(done, "DELETE FROM runtime_nodes WHERE id=$1", n.id)
			cancel()
			return
		case <-ticker.C:
		}
	}
}

func (n *Node) beat(ctx context.Context, host string) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	pool := n.Pool.Stat()
	s := Stats{
		Goroutines: runtime.NumGoroutine(), HeapMB: float64(mem.HeapAlloc) / (1 << 20), GoVersion: runtime.Version(),
		Pool:      PoolStats{Max: pool.MaxConns(), Total: pool.TotalConns(), Idle: pool.IdleConns(), Acquired: pool.AcquiredConns()},
		Providers: []provider.HostStats{}, Concurrent: n.Concurrency,
	}
	if n.Providers != nil {
		s.Providers = n.Providers.Stats()
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	beat, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = n.Pool.Exec(beat, `INSERT INTO runtime_nodes(id,role,hostname,version,started_at,stats) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET seen_at=clock_timestamp(), stats=EXCLUDED.stats`, n.id, n.Role, host, n.Version, n.started, data)
	_, _ = n.Pool.Exec(beat, "DELETE FROM runtime_nodes WHERE seen_at < clock_timestamp() - interval '1 day'")
}
