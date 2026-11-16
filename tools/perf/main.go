// Command perf seeds an isolated database with a synthetic workload and measures the
// library's read and write paths through the real store code.
//
//	go run ./tools/perf seed    (requires PERF_DATABASE_URL; destroys that database's contents)
//	go run ./tools/perf measure
package main

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/cache"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed seed.sql
var seedSQL string

//go:embed mega.sql
var megaSQL string

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	url := os.Getenv("PERF_DATABASE_URL")
	must(validPerfDatabase(url))
	ctx := context.Background()
	guard, err := database.Open(ctx, url, 1)
	must(err)
	var actual string
	must(guard.QueryRow(ctx, "SELECT current_database()").Scan(&actual))
	guard.Close()
	if actual != "pastime_perf" {
		must(fmt.Errorf("refusing database other than pastime_perf"))
	}
	switch {
	case len(os.Args) == 2 && (os.Args[1] == "seed" || os.Args[1] == "seed-mega"):
		must(database.Migrate(ctx, url))
		pool, err := database.Open(ctx, url, 4)
		must(err)
		start := time.Now()
		_, err = pool.Exec(ctx, "SET statement_timeout=0")
		must(err)
		conn, err := pool.Acquire(ctx)
		must(err)
		script := seedSQL
		if os.Args[1] == "seed-mega" {
			script = megaSQL
		}
		_, err = conn.Exec(ctx, "SET statement_timeout=0; "+script)
		must(err)
		conn.Release()
		var users, catalogRows, entries, plays int
		must(pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM catalog_items),(SELECT count(*) FROM library_entries),(SELECT count(*) FROM plays)").Scan(&users, &catalogRows, &entries, &plays))
		fmt.Printf("seeded in %s: %d users, %d catalog rows, %d entries, %d plays\n", time.Since(start).Round(time.Second), users, catalogRows, entries, plays)
		storage(ctx, pool)
	case len(os.Args) == 2 && os.Args[1] == "migrate":
		start := time.Now()
		must(database.Migrate(ctx, url))
		fmt.Printf("migrated in %s\n", time.Since(start).Round(time.Millisecond))
	case len(os.Args) == 2 && os.Args[1] == "measure":
		pool, err := database.Open(ctx, url, 16)
		must(err)
		measure(ctx, pool)
	default:
		must(fmt.Errorf("usage: perf seed|seed-mega|migrate|measure"))
	}
}

// perfUser is the account measured: the 10k-title heavy account, or PERF_USER such as
// perf_mega (250k titles).
func perfUser() string {
	if u := os.Getenv("PERF_USER"); u != "" {
		return u
	}
	return "perf_user_1"
}

// storage reports table sizes and bytes per row, including indexes, for capacity planning.
func storage(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Printf("%-18s %12s %12s %10s\n", "table", "rows", "total size", "bytes/row")
	for _, table := range []string{"library_entries", "plays", "activity_events", "play_days", "change_log", "catalog_items"} {
		var rows, bytes int64
		must(pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM "+table+"), pg_total_relation_size('"+table+"')").Scan(&rows, &bytes))
		per := int64(0)
		if rows > 0 {
			per = bytes / rows
		}
		fmt.Printf("%-18s %12d %11dM %10d\n", table, rows, bytes>>20, per)
	}
}

// session creates a real browser session for the measured account so writes go through
// the same authorization path as production requests.
func session(ctx context.Context, pool *pgxpool.Pool) identity.Actor {
	raw := "fn_s_" + strings.Repeat("p", 43)
	_, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,auth_epoch,expires_at) SELECT id,$1,auth_epoch,now()+interval '1 day' FROM users WHERE username=$2
		ON CONFLICT(token_hash) DO UPDATE SET user_id=EXCLUDED.user_id,auth_epoch=EXCLUDED.auth_epoch,expires_at=EXCLUDED.expires_at,revoked_at=NULL`, identity.Digest(raw), perfUser())
	must(err)
	a, err := identity.New(pool).Authenticate(ctx, raw, "session")
	must(err)
	return a
}

type result struct {
	name          string
	p50, p95, p99 time.Duration
}

func run(name string, n int, op func(i int) error) result {
	var samples []time.Duration
	for i := 0; i < n+10; i++ {
		start := time.Now()
		must(op(i))
		if i >= 10 { // The first ten warm caches and plans.
			samples = append(samples, time.Since(start))
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	at := func(q float64) time.Duration { return samples[int(q*float64(len(samples)-1))] }
	return result{name, at(0.5), at(0.95), at(0.99)}
}

func measure(ctx context.Context, pool *pgxpool.Pool) {
	a := session(ctx, pool)
	lib := library.Store{Pool: pool}
	cat := &catalog.Service{Pool: pool}
	valkey, err := cache.Open(os.Getenv("VALKEY_URL"))
	must(err)
	cached := library.Store{Pool: pool, Cache: cache.Bytes{Client: valkey}}
	var seriesEntry, movieEntry int64
	must(pool.QueryRow(ctx, "SELECT id FROM library_entries WHERE user_id=$1 AND media_type='series' AND status='in_progress' ORDER BY id LIMIT 1", a.User.ID).Scan(&seriesEntry))
	must(pool.QueryRow(ctx, "SELECT id FROM library_entries WHERE user_id=$1 AND media_type='movie' ORDER BY id LIMIT 1", a.User.ID).Scan(&movieEntry))
	var cursors []string
	page, err := lib.History(ctx, a, library.HistoryFilter{Limit: 50})
	must(err)
	for i := 0; i < 20 && page.NextCursor != nil; i++ {
		cursors = append(cursors, *page.NextCursor)
		page, err = lib.History(ctx, a, library.HistoryFilter{Limit: 50, Cursor: *page.NextCursor})
		must(err)
	}
	var libCursor string
	entries, err := lib.Entries(ctx, a, library.Filter{Limit: 100})
	must(err)
	var deepCursor string
	for i := 0; i < 2000 && entries.NextCursor != nil; i++ {
		if i == 20 {
			libCursor = deepCursor
		}
		deepCursor = *entries.NextCursor
		entries, err = lib.Entries(ctx, a, library.Filter{Limit: 100, Cursor: libCursor})
		must(err)
	}
	var untracked []int64
	rows, err := pool.Query(ctx, "SELECT c.id FROM catalog_items c WHERE c.parent_id IS NULL AND NOT EXISTS(SELECT 1 FROM library_entries e WHERE e.user_id=$1 AND e.catalog_item_id=c.id) ORDER BY c.id LIMIT 60", a.User.ID)
	must(err)
	for rows.Next() {
		var id int64
		must(rows.Scan(&id))
		untracked = append(untracked, id)
	}
	rows.Close()
	key := func() string {
		var k string
		must(pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&k))
		return k
	}
	now := time.Now()
	results := []result{
		run("library: first page", 50, func(int) error { _, err := lib.Entries(ctx, a, library.Filter{Limit: 30}); return err }),
		run("library: status filter", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Status: "in_progress"})
			return err
		}),
		run("library: type filter", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 30, MediaType: "book"})
			return err
		}),
		run("library: title search", 50, func(int) error { _, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Query: "Movie"}); return err }),
		run("library: misspelled search", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Query: "Moive 1797"})
			return err
		}),
		run("library: accent-folded search", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Query: "Synthétic Anime"})
			return err
		}),
		run("library: page 20", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 100, Cursor: libCursor})
			return err
		}),
		run("library: page 2000", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 100, Cursor: deepCursor})
			return err
		}),
		run("library: sort by title", 50, func(int) error { _, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Sort: "title"}); return err }),
		run("library: sort by rating", 50, func(int) error { _, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Sort: "rating"}); return err }),
		run("library: genre filter", 50, func(int) error {
			_, err := lib.Entries(ctx, a, library.Filter{Limit: 30, Genres: []string{"Horror"}})
			return err
		}),
		run("changes: from start", 20, func(int) error {
			_, err := lib.Changes(ctx, a, "1."+strconv.FormatInt(now.Unix(), 36), 200)
			return err
		}),
		run("entry detail", 50, func(int) error { _, err := lib.Get(ctx, a, seriesEntry); return err }),
		run("plays for a show", 50, func(int) error { _, err := lib.Plays(ctx, a, seriesEntry, 0); return err }),
		run("home", 50, func(int) error { _, err := lib.Home(ctx, a, now); return err }),
		run("history: first page", 50, func(int) error { _, err := lib.History(ctx, a, library.HistoryFilter{Limit: 50}); return err }),
		run("history: page 20", 50, func(int) error {
			_, err := lib.History(ctx, a, library.HistoryFilter{Limit: 50, Cursor: cursors[len(cursors)-1]})
			return err
		}),
		run("history: type filter", 50, func(int) error {
			_, err := lib.History(ctx, a, library.HistoryFilter{Limit: 50, MediaType: "movie"})
			return err
		}),
		run("stats: one year", 30, func(int) error { _, err := lib.Insights(ctx, a, now.AddDate(-1, 0, 0), now); return err }),
		run("stats: ten years", 30, func(int) error { _, err := lib.Insights(ctx, a, now.AddDate(-10, 0, 0), now); return err }),
		run("stats: ten years, cached", 50, func(int) error { _, err := cached.Insights(ctx, a, now.AddDate(-10, 0, 0), now); return err }),
		run("calendar: 30 days", 50, func(int) error { _, err := lib.Calendar(ctx, a, now, now.AddDate(0, 0, 30)); return err }),
		run("catalog: local search", 50, func(int) error { _, err := cat.Search(ctx, "movie", "synthetic movie 4242"); return err }),
		run("catalog: misspelled local search", 50, func(int) error { _, err := cat.Search(ctx, "movie", "sythetic moive 4242"); return err }),
		run("catalog: title page", 50, func(int) error { _, err := lib.EntryForCatalog(ctx, a, 4242); return err }),
		run("write: progress update", 50, func(i int) error {
			e, err := lib.Get(ctx, a, movieEntry)
			if err != nil {
				return err
			}
			p := i % 50
			_, err = lib.Progress(ctx, a, movieEntry, key(), library.ProgressInput{Version: e.Version, Progress: &p, Status: "in_progress"})
			return err
		}),
		run("write: track a title", 50, func(i int) error {
			_, err := lib.Track(ctx, a, key(), library.TrackInput{CatalogID: untracked[i]})
			return err
		}),
	}
	exportStart := time.Now()
	count := 0
	must(lib.Export(ctx, a, func(library.ExportEntry) error { count++; return nil }))
	exportTime := time.Since(exportStart)
	fmt.Printf("%-26s %9s %9s %9s\n", "operation", "p50", "p95", "p99")
	for _, r := range results {
		flag := ""
		limit := 100 * time.Millisecond
		if strings.HasPrefix(r.name, "write") {
			limit = 150 * time.Millisecond
		}
		if r.p95 > limit {
			flag = "  OVER TARGET"
		}
		fmt.Printf("%-26s %9s %9s %9s%s\n", r.name, r.p50.Round(100*time.Microsecond), r.p95.Round(100*time.Microsecond), r.p99.Round(100*time.Microsecond), flag)
	}
	fmt.Printf("export: %d entries in %s\n", count, exportTime.Round(time.Millisecond))
}

// Validate both the URL and effective driver configuration before any migration or seed.
func validPerfDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path != "/pastime_perf" || u.Query().Has("dbname") || u.Query().Has("database") || u.Query().Has("service") {
		return fmt.Errorf("PERF_DATABASE_URL must name the dedicated pastime_perf database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "pastime_perf" {
		return fmt.Errorf("invalid performance database configuration")
	}
	return nil
}
