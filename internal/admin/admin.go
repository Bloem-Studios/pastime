// Package admin serves the instance administration console: runtime health, the job
// queue, provider status, catalog maintenance and import runs. Nothing here reads a
// person's library, notes or history; import runs expose counts only.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/Bloem-Studios/pastime/internal/presence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool       *pgxpool.Pool
	Catalog    *catalog.Service
	Setup      Setup
	CacheProbe func(context.Context) error

	mu     sync.Mutex
	cached *Overview
	at     time.Time
}

// overviewTTL bounds how often the dashboard's aggregate queries run however many
// administrators have it open.
const overviewTTL = 10 * time.Second

type Node struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Hostname  string         `json:"hostname"`
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"started_at"`
	SeenAt    time.Time      `json:"seen_at"`
	Stats     presence.Stats `json:"stats"`
}

type Database struct {
	Version        string `json:"version"`
	SizeBytes      int64  `json:"size_bytes"`
	Connections    int    `json:"connections"`
	MaxConnections int    `json:"max_connections"`
	Migration      int64  `json:"migration"`
}

type Counts struct {
	Users          int64 `json:"users"`
	ActiveUsers    int64 `json:"active_users"`
	Entries        int64 `json:"entries"`
	Plays          int64 `json:"plays"`
	CatalogTitles  int64 `json:"catalog_titles"`
	CatalogRecords int64 `json:"catalog_records"`
	ActiveImports  int64 `json:"active_imports"`
	Connections    int64 `json:"connections"`
}

type JobHour struct {
	Hour      time.Time `json:"hour"`
	Succeeded int64     `json:"succeeded"`
	Failed    int64     `json:"failed"`
}

type JobHealth struct {
	Queued        int64     `json:"queued"`
	Running       int64     `json:"running"`
	Failed        int64     `json:"failed"`
	Blocked       int64     `json:"blocked"`
	OldestQueued  *int64    `json:"oldest_queued_seconds"`
	Throughput    []JobHour `json:"throughput"`
	FailedLastDay int64     `json:"failed_last_day"`
}

type Provider struct {
	Name         string                     `json:"name"`
	Configured   bool                       `json:"configured"`
	Kinds        []string                   `json:"kinds"`
	Requests     int64                      `json:"requests"`
	Failures     int64                      `json:"failures"`
	RateLimited  int64                      `json:"rate_limited"`
	MeanLatency  *int64                     `json:"mean_latency_ms"`
	LastAt       *time.Time                 `json:"last_at"`
	LastFailure  *time.Time                 `json:"last_failure_at"`
	CredentialOf string                     `json:"credential"`
	Defaults     []string                   `json:"defaults"`
	Search       provider.QueryCapabilities `json:"search"`
	Browse       provider.QueryCapabilities `json:"browse"`
	Health       string                     `json:"health"`
	SetupHint    string                     `json:"setup_hint"`
}

type Overview struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Nodes       []Node     `json:"nodes"`
	Database    Database   `json:"database"`
	Counts      Counts     `json:"counts"`
	Jobs        JobHealth  `json:"jobs"`
	Providers   []Provider `json:"providers"`
}

func require(a identity.Actor) error {
	if !a.IsAdmin() {
		return fault.Forbidden
	}
	return nil
}

// Overview is the dashboard in one response. Aggregates are cached briefly.
func (s *Store) Overview(ctx context.Context, a identity.Actor) (Overview, error) {
	if err := require(a); err != nil {
		return Overview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Since(s.at) < overviewTTL {
		return *s.cached, nil
	}
	out, err := s.overview(ctx)
	if err != nil {
		return out, err
	}
	s.cached, s.at = &out, time.Now()
	return out, nil
}

func (s *Store) overview(ctx context.Context) (Overview, error) {
	out := Overview{GeneratedAt: time.Now().UTC(), Nodes: []Node{}, Providers: []Provider{}, Jobs: JobHealth{Throughput: []JobHour{}}}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id::text,role,hostname,version,started_at,seen_at,stats FROM runtime_nodes
		WHERE seen_at > clock_timestamp() - $1::interval ORDER BY role, started_at`, strconv.Itoa(int(3*presence.Interval/time.Second))+" seconds")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var n Node
		var raw []byte
		if err = rows.Scan(&n.ID, &n.Role, &n.Hostname, &n.Version, &n.StartedAt, &n.SeenAt, &raw); err != nil {
			rows.Close()
			return out, err
		}
		_ = json.Unmarshal(raw, &n.Stats)
		out.Nodes = append(out.Nodes, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT current_setting('server_version'), pg_database_size(current_database()),
		(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()), current_setting('max_connections')::int,
		COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied), 0)`).
		Scan(&out.Database.Version, &out.Database.SizeBytes, &out.Database.Connections, &out.Database.MaxConnections, &out.Database.Migration); err != nil {
		return out, err
	}
	// Large tables use the planner's estimate; exact counts on them would dominate the
	// dashboard's cost. Small tables are counted exactly.
	estimate := func(table, exact string) (int64, error) {
		var n int64
		if err := tx.QueryRow(ctx, "SELECT reltuples::bigint FROM pg_class WHERE oid=$1::regclass", table).Scan(&n); err != nil {
			return 0, err
		}
		if n >= 200000 {
			return n, nil
		}
		err := tx.QueryRow(ctx, exact).Scan(&n)
		return n, err
	}
	c := &out.Counts
	for _, q := range []struct {
		dst          *int64
		table, exact string
	}{
		{&c.Entries, "library_entries", "SELECT count(*) FROM library_entries"},
		{&c.Plays, "plays", "SELECT count(*) FROM plays"},
		{&c.CatalogRecords, "catalog_items", "SELECT count(*) FROM catalog_items"},
	} {
		if *q.dst, err = estimate(q.table, q.exact); err != nil {
			return out, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM users WHERE active),
		(SELECT count(*) FROM catalog_items WHERE parent_id IS NULL),
		(SELECT count(*) FROM import_runs WHERE status IN ('queued','running')),
		(SELECT count(*) FROM connections)`).Scan(&c.Users, &c.ActiveUsers, &c.CatalogTitles, &c.ActiveImports, &c.Connections); err != nil {
		return out, err
	}
	j := &out.Jobs
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM jobs WHERE status='queued'), (SELECT count(*) FROM jobs WHERE status='running'),
		(SELECT count(*) FROM jobs WHERE status='failed'), (SELECT count(*) FROM jobs WHERE status='blocked'),
		(SELECT extract(epoch FROM clock_timestamp()-min(run_after))::bigint FROM jobs WHERE status='queued' AND run_after<=clock_timestamp()),
		(SELECT count(*) FROM jobs WHERE status='failed' AND updated_at > clock_timestamp()-interval '1 day')`).
		Scan(&j.Queued, &j.Running, &j.Failed, &j.Blocked, &j.OldestQueued, &j.FailedLastDay); err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT h, count(*) FILTER (WHERE j.status='succeeded'), count(*) FILTER (WHERE j.status IN ('failed','blocked'))
		FROM generate_series(date_trunc('hour', now()) - interval '23 hours', date_trunc('hour', now()), interval '1 hour') h
		LEFT JOIN jobs j ON j.status IN ('succeeded','failed','blocked') AND j.updated_at >= h AND j.updated_at < h + interval '1 hour'
		GROUP BY h ORDER BY h`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var h JobHour
		if err = rows.Scan(&h.Hour, &h.Succeeded, &h.Failed); err != nil {
			rows.Close()
			return out, err
		}
		j.Throughput = append(j.Throughput, h)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Providers = s.providerStatus(out.Nodes)
	return out, tx.Commit(ctx)
}

func (s *Store) providerStatus(nodes []Node) []Provider {
	cat := s.Catalog
	if cat == nil {
		cat = &catalog.Service{}
	}
	inventory := cat.Inventory()
	choices := cat.Choices()
	out := make([]Provider, 0, len(inventory))
	for _, p := range inventory {
		v := Provider{Name: p.Name, CredentialOf: strings.Join(p.Settings, ", "), Kinds: []string{}, Defaults: []string{}, Health: "unknown", Search: provider.QueryCapabilities{Sorts: []string{}}, Browse: provider.QueryCapabilities{Sorts: []string{}}}
		for _, c := range choices {
			if c.Name == p.Name {
				v.Configured = c.Configured
				v.Kinds = c.Kinds
				v.Defaults = c.Defaults
				v.Search = c.Search
				v.Browse = c.Browse
			}
		}
		if !v.Configured {
			v.Health = "not_configured"
			v.SetupHint = "Configure " + strings.Join(p.Settings, " and ") + " to enable this adapter."
			if len(p.Settings) == 0 {
				v.SetupHint = "This adapter is not registered on this server."
			}
		}
		if t, ok := cat.Providers[p.Name].(provider.TVDB); ok && t.PIN == "" {
			v.SetupHint = "TVDB subscriber keys require TVDB_PIN; licensed project keys may work without a PIN."
		}
		var latency int64
		var lastStatus int
		for _, n := range nodes {
			for _, h := range n.Stats.Providers {
				for _, host := range p.Hosts {
					if h.Host != host {
						continue
					}
					v.Requests += h.Requests
					v.Failures += h.Failures
					v.RateLimited += h.RateLimited
					latency += h.TotalLatency
					if at := h.LastAt; !at.IsZero() && (v.LastAt == nil || at.After(*v.LastAt)) {
						v.LastAt = &at
						lastStatus = h.LastStatus
					}
					if at := h.LastFailure; !at.IsZero() && (v.LastFailure == nil || at.After(*v.LastFailure)) {
						v.LastFailure = &at
					}
				}
			}
		}
		if v.Requests > 0 {
			mean := latency / v.Requests
			v.MeanLatency = &mean
		}
		if v.Configured && v.LastAt != nil {
			v.Health = "available"
			if lastStatus == 0 || lastStatus == 401 || lastStatus == 403 || lastStatus == 429 || lastStatus >= 500 {
				v.Health = "temporarily_unavailable"
			}
		}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------- jobs

type Job struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	RunAfter    time.Time `json:"run_after"`
	LastError   *string   `json:"last_error"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type JobFilter struct {
	Status, Kind, Cursor string
	Limit                int
}

type KindCount struct {
	Kind    string `json:"kind"`
	Queued  int64  `json:"queued"`
	Running int64  `json:"running"`
	Done    int64  `json:"succeeded"`
	Failed  int64  `json:"failed"`
	Blocked int64  `json:"blocked"`
}

var jobStatuses = map[string]bool{"queued": true, "running": true, "succeeded": true, "failed": true, "blocked": true}

// Jobs lists jobs newest first. Payloads and dedupe keys are never exposed: they can
// identify the person the work is for.
func (s *Store) Jobs(ctx context.Context, a identity.Actor, f JobFilter) (page.Result[Job], error) {
	out := page.Result[Job]{Items: []Job{}}
	if err := require(a); err != nil {
		return out, err
	}
	if f.Status != "" && !jobStatuses[f.Status] {
		return out, fault.Invalid("Unknown job status.")
	}
	if len(f.Kind) > 100 {
		return out, fault.Invalid("Unknown job kind.")
	}
	limit, err := page.Limit(f.Limit)
	if err != nil {
		return out, err
	}
	scope := "jobs:" + f.Status + ":" + f.Kind
	at, key, more, err := page.DecodeKey(scope, f.Cursor)
	if err != nil {
		return out, err
	}
	if more && !uuidLike(key) {
		return out, fault.Invalid("The page cursor does not match these filters.")
	}
	query := `SELECT id::text,kind,status,attempts,max_attempts,run_after,last_error_code,created_at,updated_at FROM jobs WHERE true`
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		query += " AND " + cond + "$" + strconv.Itoa(len(args))
	}
	if f.Status != "" {
		add("status=", f.Status)
	}
	if f.Kind != "" {
		add("kind=", f.Kind)
	}
	if more {
		args = append(args, at, key)
		query += " AND (created_at,id) < ($" + strconv.Itoa(len(args)-1) + ",$" + strconv.Itoa(len(args)) + "::uuid)"
	}
	args = append(args, limit+1)
	query += " ORDER BY created_at DESC, id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.Kind, &j.Status, &j.Attempts, &j.MaxAttempts, &j.RunAfter, &j.LastError, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, j)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		next := page.EncodeKey(scope, last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	return out, nil
}

// JobKinds counts jobs by kind and status: every unfinished job, plus the last week of
// finished ones so the table stays bounded as history grows.
func (s *Store) JobKinds(ctx context.Context, a identity.Actor) ([]KindCount, error) {
	out := []KindCount{}
	if err := require(a); err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT kind, count(*) FILTER (WHERE status='queued'), count(*) FILTER (WHERE status='running'),
		count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='failed'), count(*) FILTER (WHERE status='blocked')
		FROM (SELECT kind,status FROM jobs WHERE status IN ('queued','running','failed','blocked')
		      UNION ALL SELECT kind,status FROM jobs WHERE status='succeeded' AND updated_at > clock_timestamp()-interval '7 days') j
		GROUP BY kind ORDER BY kind`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k KindCount
		if err = rows.Scan(&k.Kind, &k.Queued, &k.Running, &k.Done, &k.Failed, &k.Blocked); err != nil {
			return out, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RetryJob requeues a failed or blocked job with a fresh attempt budget. A blocked job
// was stopped because replaying it might repeat an outside effect; retrying it is the
// administrator's decision, and it is audited.
func (s *Store) RetryJob(ctx context.Context, a identity.Actor, id string) (Job, error) {
	return s.settle(ctx, a, id, "job.retry", `UPDATE jobs SET status='queued', attempts=0, run_after=clock_timestamp(), last_error_code=NULL, updated_at=clock_timestamp()
		WHERE id=$1::uuid AND status IN ('failed','blocked')`)
}

// CancelJob stops a job that has not started or is blocked. Running work cannot be
// cancelled: its lease owner decides the outcome.
func (s *Store) CancelJob(ctx context.Context, a identity.Actor, id string) (Job, error) {
	return s.settle(ctx, a, id, "job.cancel", `UPDATE jobs SET status='failed', last_error_code='cancelled_by_admin', updated_at=clock_timestamp()
		WHERE id=$1::uuid AND status IN ('queued','blocked')`)
}

func (s *Store) settle(ctx context.Context, a identity.Actor, id, action, update string) (Job, error) {
	var j Job
	if err := require(a); err != nil {
		return j, err
	}
	if !uuidLike(id) {
		return j, fault.NotFound
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, update, id)
	if err != nil {
		return j, err
	}
	err = tx.QueryRow(ctx, `SELECT id::text,kind,status,attempts,max_attempts,run_after,last_error_code,created_at,updated_at FROM jobs WHERE id=$1::uuid`, id).
		Scan(&j.ID, &j.Kind, &j.Status, &j.Attempts, &j.MaxAttempts, &j.RunAfter, &j.LastError, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, fault.NotFound
	}
	if err != nil {
		return j, err
	}
	if tag.RowsAffected() == 0 {
		return j, &fault.Error{Status: 409, Code: "job_state", Message: "This job cannot be changed that way in its current state."}
	}
	if err = audit(ctx, tx, a, action, "job", id, map[string]string{"kind": j.Kind}); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}

func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func audit(ctx context.Context, tx pgx.Tx, a identity.Actor, action, kind, id string, meta any) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,actor_label,action,target_kind,target_id,metadata) VALUES($1,$2,$3,$4,$5,$6)`,
		a.User.ID, a.User.Username, action, kind, id, data)
	return err
}

// ---------------------------------------------------------------- catalog

type KindStats struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Titles   int64  `json:"titles"`
	Tracked  int64  `json:"tracked"`
	Stale    int64  `json:"stale"`
}

type CatalogStats struct {
	Records int64       `json:"records"`
	Kinds   []KindStats `json:"kinds"`
}

// CatalogStats summarises the shared catalog by type and provider. "Stale" counts
// tracked titles not refreshed in seven days.
func (s *Store) CatalogStats(ctx context.Context, a identity.Actor) (CatalogStats, error) {
	out := CatalogStats{Kinds: []KindStats{}}
	if err := require(a); err != nil {
		return out, err
	}
	if err := s.Pool.QueryRow(ctx, "SELECT GREATEST(reltuples::bigint,0) FROM pg_class WHERE oid='catalog_items'::regclass").Scan(&out.Records); err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `WITH tracked AS (SELECT DISTINCT catalog_item_id AS id FROM library_entries WHERE catalog_item_id IS NOT NULL)
		SELECT c.kind, c.provider, count(*), count(t.id), count(t.id) FILTER (WHERE c.refreshed_at < clock_timestamp()-interval '7 days')
		FROM catalog_items c LEFT JOIN tracked t ON t.id=c.id
		WHERE c.parent_id IS NULL GROUP BY c.kind, c.provider ORDER BY count(*) DESC`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k KindStats
		if err = rows.Scan(&k.Kind, &k.Provider, &k.Titles, &k.Tracked, &k.Stale); err != nil {
			return out, err
		}
		out.Kinds = append(out.Kinds, k)
	}
	return out, rows.Err()
}

// RefreshStale schedules a metadata refresh for tracked titles that are due, now
// instead of at the next scheduler pass.
func (s *Store) RefreshStale(ctx context.Context, a identity.Actor) (int, error) {
	if err := require(a); err != nil {
		return 0, err
	}
	if s.Catalog == nil {
		return 0, fault.Unavailable
	}
	n, err := s.Catalog.EnqueueStale(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return n, err
	}
	defer tx.Rollback(ctx)
	if err = audit(ctx, tx, a, "catalog.refresh_stale", "catalog", "stale", map[string]int{"scheduled": n}); err != nil {
		return n, err
	}
	return n, tx.Commit(ctx)
}

// ---------------------------------------------------------------- imports

type ImportRun struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Source     string     `json:"source"`
	Status     string     `json:"status"`
	Total      int        `json:"total"`
	Processed  int        `json:"processed"`
	Imported   int        `json:"imported"`
	PlaysAdded int        `json:"plays_added"`
	Unmatched  int        `json:"unmatched"`
	Failed     int        `json:"failed"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Imports lists every account's import runs newest first, as counts only.
func (s *Store) Imports(ctx context.Context, a identity.Actor, status, cursor string, limit int) (page.Result[ImportRun], error) {
	out := page.Result[ImportRun]{Items: []ImportRun{}}
	if err := require(a); err != nil {
		return out, err
	}
	switch status {
	case "", "queued", "running", "succeeded", "failed", "cancelled", "rolled_back":
	default:
		return out, fault.Invalid("Unknown import status.")
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "imports:" + status
	at, key, more, err := page.DecodeKey(scope, cursor)
	if err != nil {
		return out, err
	}
	if more && !uuidLike(key) {
		return out, fault.Invalid("The page cursor does not match these filters.")
	}
	query := `SELECT r.id::text,u.username,r.source,r.status,r.total,r.processed,r.imported,r.plays_added,r.unmatched,r.failed,r.created_at,r.finished_at
		FROM import_runs r JOIN users u ON u.id=r.user_id WHERE true`
	args := []any{}
	if status != "" {
		args = append(args, status)
		query += " AND r.status=$1"
	}
	if more {
		args = append(args, at, key)
		query += " AND (r.created_at,r.id) < ($" + strconv.Itoa(len(args)-1) + ",$" + strconv.Itoa(len(args)) + "::uuid)"
	}
	args = append(args, limit+1)
	query += " ORDER BY r.created_at DESC, r.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ImportRun
		if err = rows.Scan(&r.ID, &r.Username, &r.Source, &r.Status, &r.Total, &r.Processed, &r.Imported, &r.PlaysAdded, &r.Unmatched, &r.Failed, &r.CreatedAt, &r.FinishedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		next := page.EncodeKey(scope, last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	return out, nil
}

// Setup describes the server configuration the checks inspect. It carries no secrets.
type Setup struct {
	Origin                                 string
	SecretKey, Valkey, OIDC, NotifyPrivate bool
}

type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok, warning, error
	Detail string `json:"detail"`
}

// Checks reports the configuration an operator should review, with what to change.
func (s *Store) Checks(ctx context.Context, a identity.Actor) ([]Check, error) {
	if err := require(a); err != nil {
		return nil, err
	}
	var out []Check
	add := func(id, title, status, detail string) { out = append(out, Check{id, title, status, detail}) }
	if strings.HasPrefix(s.Setup.Origin, "https://") {
		add("origin", "Served over HTTPS", "ok", "Cookies are marked Secure and bound to "+s.Setup.Origin+".")
	} else {
		add("origin", "Served over HTTPS", "warning", "PUBLIC_ORIGIN is "+s.Setup.Origin+". Put the server behind HTTPS before inviting anyone.")
	}
	if s.Setup.SecretKey {
		add("secret_key", "Secret key", "ok", "Notification destinations and authenticator secrets are encrypted at rest.")
	} else {
		add("secret_key", "Secret key", "warning", "Set SECRET_KEY (32 random bytes, base64) to enable notifications and authenticator recovery.")
	}
	if s.Setup.Valkey {
		if s.CacheProbe == nil {
			add("valkey", "Cache", "warning", "Configured; reachability has not been checked.")
		} else {
			probe, cancel := context.WithTimeout(ctx, time.Second)
			err := s.CacheProbe(probe)
			cancel()
			if err != nil {
				add("valkey", "Cache", "error", "The configured cache did not answer its health check.")
			} else {
				add("valkey", "Cache", "ok", "The cache answered its health check. PostgreSQL owns authentication limits.")
			}
		}
	} else {
		add("valkey", "Cache", "warning", "VALKEY_URL is not set; provider searches are not cached.")
	}
	if s.Setup.OIDC {
		add("oidc", "Single sign-on", "ok", "OpenID Connect sign-in is configured.")
	} else {
		add("oidc", "Single sign-on", "ok", "Optional. Set OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET to offer single sign-on.")
	}
	if s.Setup.NotifyPrivate {
		add("notify_private", "Notification network access", "warning", "NOTIFY_ALLOW_PRIVATE lets members send notices to addresses on your private network.")
	} else {
		add("notify_private", "Notification network access", "ok", "Notices cannot reach private network addresses.")
	}
	var workers int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM runtime_nodes WHERE role='worker' AND seen_at > clock_timestamp()-interval '90 seconds'").Scan(&workers); err != nil {
		return nil, err
	}
	if workers > 0 {
		add("worker", "Background worker", "ok", strconv.Itoa(workers)+" running.")
	} else {
		add("worker", "Background worker", "error", "No worker has reported in the last 90 seconds. Imports, metadata refreshes, scrobbles and notices wait until one runs.")
	}
	configured := 0
	if s.Catalog != nil {
		configured = len(s.Catalog.Providers)
	}
	if s.Catalog != nil && s.Catalog.Providers["tmdb"] == nil {
		add("tmdb", "Film and TV metadata", "warning", "TMDB_API_KEY is not set: film and TV search uses titles already on this server, and media server scrobbles cannot be matched.")
	} else {
		add("tmdb", "Metadata providers", "ok", strconv.Itoa(configured)+" providers enabled.")
	}
	add("backups", "Backups", "warning", "Backup freshness and restore success are not verified by this server. Back up PostgreSQL and escrow the encryption keys separately; rehearse restoration in isolation.")
	return out, nil
}
