// Package exports writes library exports, either streamed to the person or kept on the
// server as scheduled or on-demand snapshots.
package exports

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/transfer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is the durable job kind that writes one scheduled snapshot.
const Job = "scheduled_export"

// Formats maps each export format to its media type and file extension.
var Formats = map[string]struct{ ContentType, Ext string }{
	"jsonl":      {"application/x-ndjson", "jsonl"},
	"csv":        {"text/csv; charset=utf-8", "csv"},
	"letterboxd": {"text/csv; charset=utf-8", "csv"},
}

// formatScopes are the scopes beyond library:read that exporting a format needs. Its
// stored snapshots need them too, although a wider worker wrote them.
var formatScopes = map[string][]string{"jsonl": {"lists:read"}}

// readable lists the stored formats the actor may list and download.
func readable(a identity.Actor) []string {
	out := []string{}
	for format := range Formats {
		ok := a.Allows("library:read")
		for _, scope := range formatScopes[format] {
			ok = ok && a.Allows(scope)
		}
		if ok {
			out = append(out, format)
		}
	}
	return out
}

// SafeCell stops a spreadsheet from running a cell as a formula.
func SafeCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func optional(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// Write streams the caller's library in one format from a consistent snapshot and
// reports how many titles it wrote.
func Write(ctx context.Context, lib library.Store, a identity.Actor, format string, w io.Writer) (int, error) {
	count := 0
	switch format {
	case "jsonl":
		return transfer.Write(ctx, lib.Pool, a, w)
	case "csv":
		c := csv.NewWriter(w)
		_ = c.Write([]string{"title", "type", "year", "status", "progress", "total", "unit", "rating", "plays", "last_played", "provider", "provider_id", "added", "notes"})
		err := lib.Export(ctx, a, func(e library.ExportEntry) error {
			count++
			last := ""
			if n := len(e.Plays); n > 0 {
				last = e.Plays[n-1].At.UTC().Format(time.RFC3339)
			}
			return c.Write([]string{SafeCell(e.Title), e.Kind, optional(e.Year), e.Status, strconv.Itoa(e.Progress), optional(e.Total), e.Unit, optional(e.Rating),
				strconv.Itoa(len(e.Plays)), last, e.Provider, e.ProviderID, e.Added.UTC().Format(time.RFC3339), SafeCell(e.Notes)})
		})
		c.Flush()
		if err == nil {
			err = c.Error()
		}
		return count, err
	case "letterboxd":
		// Letterboxd's import format: one row per viewing, five-star ratings, later
		// viewings marked as rewatches.
		c := csv.NewWriter(w)
		_ = c.Write([]string{"Title", "Year", "tmdbID", "WatchedDate", "Rating", "Rewatch"})
		err := lib.Export(ctx, a, func(e library.ExportEntry) error {
			if e.Kind != "movie" {
				return nil
			}
			count++
			tmdb := ""
			if e.Provider == "tmdb" {
				tmdb = e.ProviderID
			}
			rating := ""
			if e.Rating != nil && *e.Rating > 0 {
				rating = strconv.FormatFloat(float64(*e.Rating)/2, 'f', -1, 64)
			}
			if len(e.Plays) == 0 {
				if e.Status == "completed" {
					return c.Write([]string{SafeCell(e.Title), optional(e.Year), tmdb, "", rating, "false"})
				}
				return nil
			}
			for i, p := range e.Plays {
				if err := c.Write([]string{SafeCell(e.Title), optional(e.Year), tmdb, p.At.UTC().Format("2006-01-02"), rating, strconv.FormatBool(i > 0)}); err != nil {
					return err
				}
			}
			return nil
		})
		c.Flush()
		if err == nil {
			err = c.Error()
		}
		return count, err
	}
	return 0, fault.Invalid("Unsupported export format.")
}

// ---------------------------------------------------------------- stored snapshots

type Store struct {
	Pool    *pgxpool.Pool
	Library library.Store
}

type Schedule struct {
	Frequency string    `json:"frequency"`
	Format    string    `json:"format"`
	Keep      int       `json:"keep"`
	NextRunAt time.Time `json:"next_run_at"`
	Version   int       `json:"version"`
	AnchorDay int       `json:"-"`
}

type File struct {
	ID        string    `json:"id"`
	Format    string    `json:"format"`
	Trigger   string    `json:"trigger"`
	Entries   int       `json:"entries"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// maxSnapshot caps one stored export after compression.
const maxSnapshot = 64 << 20

func next(frequency string, from time.Time, preferredDay ...int) time.Time {
	switch frequency {
	case "daily":
		return from.Add(24 * time.Hour)
	case "monthly":
		day := from.Day()
		if len(preferredDay) > 0 && preferredDay[0] > 0 {
			day = preferredDay[0]
		}
		month := time.Date(from.Year(), from.Month()+1, 1, from.Hour(), from.Minute(), from.Second(), from.Nanosecond(), from.Location())
		last := month.AddDate(0, 1, -1).Day()
		if day > last {
			day = last
		}
		return month.AddDate(0, 0, day-1)
	default:
		return from.AddDate(0, 0, 7)
	}
}

func (s Store) Schedule(ctx context.Context, a identity.Actor) (*Schedule, error) {
	if !a.Browser() {
		return nil, fault.Forbidden
	}
	var out Schedule
	err := s.Pool.QueryRow(ctx, "SELECT frequency,format,keep,next_run_at,version FROM export_schedules WHERE profile_id=$1", a.TrackingID()).Scan(&out.Frequency, &out.Format, &out.Keep, &out.NextRunAt, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}

// SaveSchedule creates or replaces the schedule; the first snapshot runs within the hour.
func (s Store) SaveSchedule(ctx context.Context, a identity.Actor, in Schedule) (Schedule, error) {
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Schedule, error) {
		if !a.Browser() {
			return in, fault.Forbidden
		}
		if (in.Frequency != "daily" && in.Frequency != "weekly" && in.Frequency != "monthly") || Formats[in.Format].Ext == "" || in.Keep < 1 || in.Keep > 20 {
			return in, fault.Invalid("Choose daily, weekly or monthly, a format, and keep 1 to 20 snapshots.")
		}
		err := tx.QueryRow(ctx, `INSERT INTO export_schedules(profile_id,frequency,format,keep,next_run_at,anchor_day) VALUES($1,$2,$3,$4,clock_timestamp(),extract(day FROM clock_timestamp() AT TIME ZONE 'UTC'))
		ON CONFLICT (profile_id) DO UPDATE SET frequency=EXCLUDED.frequency,format=EXCLUDED.format,keep=EXCLUDED.keep,version=export_schedules.version+1
		RETURNING next_run_at,version`, a.TrackingID(), in.Frequency, in.Format, in.Keep).Scan(&in.NextRunAt, &in.Version)
		return in, err
	})
}

func (s Store) DeleteSchedule(ctx context.Context, a identity.Actor) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		_, err := tx.Exec(ctx, "DELETE FROM export_schedules WHERE profile_id=$1", a.TrackingID())
		return err
	})
}

func (s Store) Files(ctx context.Context, a identity.Actor) ([]File, error) {
	out := []File{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT id::text,format,trigger,entries,size_bytes,created_at FROM export_files WHERE profile_id=$1 AND format=ANY($2) ORDER BY created_at DESC LIMIT 50", a.TrackingID(), readable(a))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		if err = rows.Scan(&f.ID, &f.Format, &f.Trigger, &f.Entries, &f.SizeBytes, &f.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Open returns a stored snapshot, decompressed as it is read.
func (s Store) Open(ctx context.Context, a identity.Actor, id string) (File, io.ReadCloser, error) {
	var f File
	if !a.Allows("library:read") {
		return f, nil, fault.Forbidden
	}
	if !identity.ValidUUID(id) {
		return f, nil, fault.NotFound
	}
	var data []byte
	err := s.Pool.QueryRow(ctx, "SELECT id::text,format,trigger,entries,size_bytes,created_at,data FROM export_files WHERE profile_id=$1 AND id=$2::uuid", a.TrackingID(), id).
		Scan(&f.ID, &f.Format, &f.Trigger, &f.Entries, &f.SizeBytes, &f.CreatedAt, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, nil, fault.NotFound
	}
	if err != nil {
		return f, nil, err
	}
	if !slices.Contains(readable(a), f.Format) {
		return File{}, nil, fault.Forbidden
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	return f, r, err
}

func (s Store) DeleteFile(ctx context.Context, a identity.Actor, id string) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		if !identity.ValidUUID(id) {
			return fault.NotFound
		}
		tag, err := tx.Exec(ctx, "DELETE FROM export_files WHERE profile_id=$1 AND id=$2::uuid", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return err
	})
}

// Snapshot writes and keeps one export, then trims older snapshots beyond keep.
func (s Store) Snapshot(ctx context.Context, userID int64, format, trigger string, keep int) (File, error) {
	return s.snapshot(ctx, userID, format, trigger, keep, nil)
}
func (s Store) snapshot(ctx context.Context, userID int64, format, trigger string, keep int, due *time.Time) (File, error) {
	var f File
	a, err := identity.ResolveWorkerProfile(ctx, s.Pool, userID, "export", []string{"library:read", "lists:read"}, false)
	if err != nil {
		return f, fault.Unauthenticated
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&snapshotWriter{buf: &buf})
	count, err := Write(ctx, s.Library, a, format, zw)
	if err != nil {
		return f, err
	}
	if err = zw.Close(); err != nil {
		return f, err
	}
	if buf.Len() > maxSnapshot {
		return f, fault.Invalid("This library is too large to keep as a snapshot. Download it directly instead.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return f, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u", userID).Scan(&active); err != nil {
		return f, err
	}
	if !active {
		return f, fault.Unauthenticated
	}
	if due != nil {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pastime.export.' || $1::bigint::text,0))", userID); err != nil {
			return f, err
		}
		err = tx.QueryRow(ctx, "SELECT id::text,format,trigger,entries,size_bytes,created_at FROM export_files WHERE profile_id=$1 AND schedule_due=$2", userID, due).Scan(&f.ID, &f.Format, &f.Trigger, &f.Entries, &f.SizeBytes, &f.CreatedAt)
		if err == nil {
			return f, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return f, err
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO export_files(profile_id,format,trigger,entries,size_bytes,data,schedule_due) VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING id::text,format,trigger,entries,size_bytes,created_at`, userID, format, trigger, count, buf.Len(), buf.Bytes(), due).
		Scan(&f.ID, &f.Format, &f.Trigger, &f.Entries, &f.SizeBytes, &f.CreatedAt); err != nil {
		return f, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM export_files WHERE profile_id=$1 AND id IN (SELECT id FROM export_files WHERE profile_id=$1 ORDER BY created_at DESC OFFSET $2)`, userID, keep); err != nil {
		return f, err
	}
	return f, tx.Commit(ctx)
}

// SnapshotNow keeps an on-demand snapshot next to the scheduled ones.
func (s Store) SnapshotNow(ctx context.Context, a identity.Actor, format string) (File, error) {
	if !a.Browser() {
		return File{}, fault.Forbidden
	}
	if Formats[format].Ext == "" {
		return File{}, fault.Invalid("Unsupported export format.")
	}
	keep := 5
	if sched, err := s.Schedule(ctx, a); err == nil && sched != nil {
		keep = sched.Keep
	}
	return s.Snapshot(ctx, a.TrackingID(), format, "manual", keep)
}

// EnqueueDue schedules a job for every schedule that is due. The dedupe key includes the
// due time, so each period runs once however many schedulers are running.
func (s Store) EnqueueDue(ctx context.Context, enqueue func(ctx context.Context, key string, payload []byte) error) (int, error) {
	rows, err := s.Pool.Query(ctx, "SELECT profile_id,next_run_at FROM export_schedules WHERE next_run_at <= clock_timestamp() LIMIT 500")
	if err != nil {
		return 0, err
	}
	type due struct {
		user int64
		at   time.Time
	}
	var list []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.user, &d.at); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, d := range list {
		payload, _ := json.Marshal(map[string]any{"profile_id": d.user, "due": d.at})
		if err = enqueue(ctx, "export:"+strconv.FormatInt(d.user, 10)+":"+strconv.FormatInt(d.at.Unix(), 10), payload); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// Handler writes one scheduled snapshot and advances the schedule.
func (s Store) Handler(ctx context.Context, raw json.RawMessage) error {
	var p struct {
		ProfileID int64     `json:"profile_id"`
		Due       time.Time `json:"due"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ProfileID < 1 || p.Due.IsZero() {
		return jobs.ErrInvalidPayload
	}
	var sched Schedule
	err := s.Pool.QueryRow(ctx, "SELECT frequency,format,keep,next_run_at,anchor_day FROM export_schedules WHERE profile_id=$1", p.ProfileID).Scan(&sched.Frequency, &sched.Format, &sched.Keep, &sched.NextRunAt, &sched.AnchorDay)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && sched.NextRunAt.After(p.Due.Add(time.Second))) {
		return nil // Removed or already advanced.
	}
	if err != nil {
		return err
	}
	if _, err = s.snapshot(ctx, p.ProfileID, sched.Format, "schedule", sched.Keep, &p.Due); err != nil {
		return err // Never advance a schedule past a snapshot that was not stored.
	}
	nextRun := next(sched.Frequency, sched.NextRunAt, sched.AnchorDay)
	for !nextRun.After(time.Now()) {
		nextRun = next(sched.Frequency, nextRun, sched.AnchorDay)
	}
	_, err = s.Pool.Exec(ctx, "UPDATE export_schedules SET next_run_at=$2 WHERE profile_id=$1 AND next_run_at=$3", p.ProfileID, nextRun, sched.NextRunAt)
	return err
}

// Refuse compressed output before the backing buffer exceeds the snapshot budget.
type snapshotWriter struct{ buf *bytes.Buffer }

func (w *snapshotWriter) Write(p []byte) (int, error) {
	if len(p) > maxSnapshot-w.buf.Len() {
		return 0, fault.Invalid("This library is too large to keep as a snapshot. Download it directly instead.")
	}
	return w.buf.Write(p)
}
