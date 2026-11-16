package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/netguard"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/transfer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is the durable job kind that processes one import run.
const Job = "library_import"

// Sources lists the supported import sources.
var Sources = []string{"trakt", "letterboxd", "imdb", "goodreads", "storygraph", "anilist", "native", "yamtrack", "grouvee", "tvtime", "csv", "clz", "wetrakr", "playnite"}

type Run struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	CancelRequested bool       `json:"cancel_requested"`
	UploadName      string     `json:"upload_name"`
	Total           int        `json:"total"`
	Processed       int        `json:"processed"`
	Imported        int        `json:"imported"`
	PlaysAdded      int        `json:"plays_added"`
	Unchanged       int        `json:"unchanged"`
	Unmatched       int        `json:"unmatched"`
	Failed          int        `json:"failed"`
	UnmatchedSample []string   `json:"unmatched_sample"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

type Store struct {
	Pool    *pgxpool.Pool
	Catalog *catalog.Service
	Library library.Store
	// Box decrypts linked-service credentials; nil disables linked services.
	Box *sealed.Box
	// Creds are the instance's application keys for services that need one.
	Creds Credentials
	// AllowPrivate lets members' linked services reach private network addresses.
	// Administrators always may, since media servers usually live on the local network.
	AllowPrivate bool
	// HTTP replaces the guarded client in tests.
	HTTP *http.Client
}

const runColumns = `id::text,source,status,cancel_requested,upload_name,total,processed,imported,plays_added,unchanged,unmatched,failed,unmatched_sample,error,created_at,started_at,finished_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var sample []byte
	err := row.Scan(&r.ID, &r.Source, &r.Status, &r.CancelRequested, &r.UploadName, &r.Total, &r.Processed, &r.Imported, &r.PlaysAdded, &r.Unchanged, &r.Unmatched, &r.Failed, &sample, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fault.NotFound
	}
	r.UnmatchedSample = []string{}
	_ = json.Unmarshal(sample, &r.UnmatchedSample)
	return r, err
}

func validSource(s string) bool {
	for _, v := range Sources {
		if v == s {
			return true
		}
	}
	return false
}

// Start records a run and enqueues it. File sources carry the upload; AniList takes a
// public username instead.
func (s Store) Start(ctx context.Context, a identity.Actor, source, uploadName string, upload []byte, username string) (Run, error) {
	if !validSource(source) {
		return Run{}, fault.Invalid("Choose a supported import source.")
	}
	options := map[string]string{}
	if source == "anilist" {
		username = strings.TrimSpace(username)
		if username == "" || len(username) > 60 || strings.ContainsAny(username, " /?#") {
			return Run{}, fault.Invalid("Enter your AniList username.")
		}
		options["username"] = username
		upload = nil
	} else if len(upload) == 0 {
		return Run{}, fault.Invalid("Attach the export file.")
	}
	if len(upload) > 64<<20 {
		return Run{}, fault.Invalid("The file is larger than 64 MiB.")
	}
	if len([]rune(uploadName)) > 200 {
		uploadName = string([]rune(uploadName)[:200])
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return Run{}, err
	}
	if (source == "native" || source == "wetrakr") && !a.Allows("lists:write") {
		return Run{}, fault.Forbidden
	}
	opts, _ := json.Marshal(options)
	r, err := scanRun(tx.QueryRow(ctx, `INSERT INTO import_runs(profile_id,source,options,upload,upload_name) VALUES($1,$2,$3,$4,$5) RETURNING `+runColumns, a.TrackingID(), source, opts, upload, uploadName))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Run{}, &fault.Error{Status: 409, Code: "import_in_progress", Message: "An import is already running. Wait for it to finish or cancel it."}
	}
	if err != nil {
		return Run{}, err
	}
	payload, _ := json.Marshal(map[string]string{"run_id": r.ID})
	if _, err = jobs.Enqueue(ctx, tx, Job, "import:"+r.ID, "idempotent", payload, 3); err != nil {
		return Run{}, err
	}
	return r, tx.Commit(ctx)
}

// settle marks runs whose job exhausted its attempts as failed, so a crashed import
// never shows as running forever.
func (s Store) settle(ctx context.Context, userID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE import_runs r SET status='failed',error='The import stopped after repeated errors. Try again later.',finished_at=clock_timestamp()
		FROM jobs j WHERE r.profile_id=$1 AND r.status IN ('queued','running') AND j.dedupe_key='import:'||r.id::text AND j.status IN ('failed','blocked')`, userID)
	return err
}

func (s Store) List(ctx context.Context, a identity.Actor) ([]Run, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	if err := s.settle(ctx, a.TrackingID()); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+runColumns+" FROM import_runs WHERE profile_id=$1 ORDER BY created_at DESC LIMIT 50", a.TrackingID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s Store) Get(ctx context.Context, a identity.Actor, id string) (Run, error) {
	if !a.Allows("library:read") || !identity.ValidUUID(id) {
		return Run{}, fault.NotFound
	}
	if err := s.settle(ctx, a.TrackingID()); err != nil {
		return Run{}, err
	}
	return scanRun(s.Pool.QueryRow(ctx, "SELECT "+runColumns+" FROM import_runs WHERE profile_id=$1 AND id=$2", a.TrackingID(), id))
}

// Cancel asks a queued or running import to stop after its current record.
func (s Store) Cancel(ctx context.Context, a identity.Actor, id string) (Run, error) {
	return identity.Write(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) (Run, error) {
		if !a.Allows("library:write") || !identity.ValidUUID(id) {
			return Run{}, fault.NotFound
		}
		r, err := scanRun(tx.QueryRow(ctx, `UPDATE import_runs SET cancel_requested=true,
		status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,
		finished_at=CASE WHEN status='queued' THEN clock_timestamp() ELSE finished_at END,
		upload=CASE WHEN status='queued' THEN NULL ELSE upload END
		WHERE profile_id=$1 AND id=$2 RETURNING `+runColumns, a.TrackingID(), id))
		return r, err
	})
}

type counters struct {
	processed, imported, plays, unchanged, unmatched, failed int
	sample                                                   []string
}

// Handler processes a run. It is safe to retry: every write is a merge and every play
// carries a source identity, so records applied before a crash are not duplicated.
func (s Store) Handler(ctx context.Context, raw json.RawMessage) error {
	var p struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(raw, &p) != nil || !identity.ValidUUID(p.RunID) {
		return jobs.ErrInvalidPayload
	}
	var userID int64
	var source, status string
	var upload []byte
	var optionsRaw []byte
	err := s.Pool.QueryRow(ctx, `UPDATE import_runs SET status='running',started_at=COALESCE(started_at,clock_timestamp()),processed=0,imported=0,plays_added=0,unchanged=0,unmatched=0,failed=0,unmatched_sample='[]'
		WHERE id=$1 AND status IN ('queued','running') RETURNING profile_id,source,status,upload,options`, p.RunID).Scan(&userID, &source, &status, &upload, &optionsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // Cancelled or already finished.
	}
	if err != nil {
		return err
	}
	if source == "native" {
		upload, err = nativeBytes(upload)
		if err != nil {
			return s.finish(ctx, p.RunID, "failed", "Invalid, incomplete or unsupported native snapshot.", nil)
		}
	}
	if source == "native" && transfer.IsNative(upload) {
		_, _, restoreErr := transfer.Restore(ctx, s.Pool, userID, p.RunID, upload, s.nativeReference)
		if restoreErr != nil {
			var fe *fault.Error
			if errors.As(restoreErr, &fe) && fe.Status != 503 {
				return s.finish(ctx, p.RunID, "failed", fe.Message, nil)
			}
			return restoreErr
		}
		return nil // Restore committed the data, receipt and run outcome together.
	}
	records, err := s.parse(ctx, source, upload, optionsRaw)
	if errors.Is(err, errLinkedRun) {
		records, err = s.parseLinked(ctx, p.RunID, userID, optionsRaw)
	}
	if err != nil {
		message := "The import file could not be read."
		if errors.Is(err, ErrFormat) {
			message = strings.TrimPrefix(err.Error(), ErrFormat.Error()+": ")
		} else if errors.Is(err, provider.ErrNotConfigured) {
			message = "This service is not set up on this server."
		} else if errors.Is(err, provider.ErrNotFound) {
			message = "No public list was found for that username."
		} else if !errors.Is(err, provider.ErrInvalidPayload) {
			return err // Provider or database outage: let the queue retry.
		}
		return s.finish(ctx, p.RunID, "failed", message, nil)
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE import_runs SET total=$2 WHERE id=$1", p.RunID, len(records)); err != nil {
		return err
	}
	c := &counters{sample: []string{}}
	shows := map[int64]map[[2]int]int64{}
	for i, rec := range records {
		if i%25 == 0 {
			var cancel bool
			if err = s.Pool.QueryRow(ctx, "SELECT cancel_requested FROM import_runs WHERE id=$1", p.RunID).Scan(&cancel); err != nil {
				return err
			}
			if cancel {
				return s.finish(ctx, p.RunID, "cancelled", "", c)
			}
			if err = s.progress(ctx, p.RunID, c); err != nil {
				return err
			}
		}
		if err = s.apply(ctx, userID, p.RunID, source, rec, shows, c); err != nil {
			if errors.Is(err, fault.Unauthenticated) {
				return s.finish(ctx, p.RunID, "failed", "The account is no longer active.", c)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if keepErr := s.keepUnmatched(ctx, userID, p.RunID, rec); keepErr != nil {
				return keepErr
			}
			c.failed++
		}
		c.processed++
	}
	if c.failed > 0 || c.unmatched > 0 {
		return s.finish(ctx, p.RunID, "partial_success", "Some records could not be applied; they are retained for repair.", c)
	}
	return s.finish(ctx, p.RunID, "succeeded", "", c)
}

func (s Store) parse(ctx context.Context, source string, upload, optionsRaw []byte) ([]Record, error) {
	var linked struct {
		LinkID int64 `json:"link_id"`
		UserID int64 `json:"-"`
	}
	if json.Unmarshal(optionsRaw, &linked) == nil && linked.LinkID > 0 {
		return nil, errLinkedRun // handled by parseLinked, which knows the owner
	}
	switch source {
	case "trakt":
		return ParseTrakt(upload)
	case "letterboxd":
		return ParseLetterboxd(upload)
	case "imdb":
		return ParseIMDb(upload)
	case "goodreads":
		return ParseGoodreads(upload)
	case "storygraph":
		return ParseStoryGraph(upload)
	case "native":
		return ParseNative(upload)
	case "yamtrack":
		return ParseYamtrack(upload)
	case "grouvee":
		return ParseGrouvee(upload)
	case "tvtime":
		return ParseTVTime(upload)
	case "csv":
		return ParseGenericCSV(upload)
	case "wetrakr":
		return ParseWeTrakr(upload)
	case "clz":
		return ParseCLZ(upload)
	case "playnite":
		return ParsePlaynite(upload)
	case "anilist":
		var o struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal(optionsRaw, &o)
		a, ok := s.Catalog.Providers["anilist"].(provider.AniList)
		if !ok {
			return nil, provider.ErrNotConfigured
		}
		entries, err := a.UserLists(ctx, o.Username)
		if err != nil {
			return nil, err
		}
		return fromAniList(entries), nil
	}
	return nil, ErrFormat
}

func fromAniList(entries []provider.ListEntry) []Record {
	status := map[string]string{"CURRENT": "in_progress", "REPEATING": "in_progress", "PLANNING": "planned", "COMPLETED": "completed", "DROPPED": "dropped", "PAUSED": "paused"}
	out := make([]Record, 0, len(entries))
	for _, e := range entries {
		id := strconv.FormatInt(e.MediaID, 10)
		r := Record{Label: e.Kind + " " + id, Kind: e.Kind, Direct: &DirectRef{Provider: "anilist", Kind: e.Kind, ID: id}, Status: status[e.Status], Progress: intp(e.Progress), Notes: e.Notes}
		if e.Score > 0 {
			r.Rating = intp(int(e.Score + 0.5))
		}
		if e.CompletedAt != nil && e.Status == "COMPLETED" {
			r.Plays = []PlayRef{{At: *e.CompletedAt, ExternalID: "anilist:" + e.Kind + ":" + id + ":completed"}}
		}
		out = append(out, r)
	}
	return out
}

// resolve matches a record to its catalog title, retrying briefly through provider outages.
func (s Store) resolve(ctx context.Context, rec Record) (catalog.Item, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var item catalog.Item
		var err error
		switch {
		case rec.Direct != nil:
			item, err = s.Catalog.Resolve(ctx, rec.Direct.Provider, rec.Direct.Kind, rec.Direct.ID)
		case rec.Video != nil:
			var m catalog.Match
			m, err = s.Catalog.MatchVideo(ctx, *rec.Video)
			item = m.Title
		case rec.Book != nil:
			item, err = s.Catalog.MatchBook(ctx, *rec.Book)
		case rec.External != nil:
			item, err = s.Catalog.ResolveExternal(ctx, rec.External.Provider, rec.External.Kind, rec.External.Scheme, rec.External.Value)
			if errors.Is(err, catalog.ErrNoMatch) && rec.Title != nil {
				item, err = s.Catalog.MatchTitle(ctx, *rec.Title)
			}
		case rec.Title != nil:
			item, err = s.Catalog.MatchTitle(ctx, *rec.Title)
		default:
			return catalog.Item{}, catalog.ErrNoMatch
		}
		// An identifier the provider no longer knows falls back to the title.
		var miss *fault.Error
		if rec.Direct != nil && rec.Title != nil && errors.As(err, &miss) && (miss.Status == 404 || miss.Status == 400) {
			item, err = s.Catalog.MatchTitle(ctx, *rec.Title)
		}
		if err == nil {
			return item, nil
		}
		var fe *fault.Error
		if errors.As(err, &fe) && fe.Status != 503 {
			return catalog.Item{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return catalog.Item{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
	return catalog.Item{}, lastErr
}

func (s Store) apply(ctx context.Context, userID int64, runID, source string, rec Record, shows map[int64]map[[2]int]int64, c *counters) error {
	item, err := s.resolve(ctx, rec)
	if err != nil {
		var fe *fault.Error
		if errors.As(err, &fe) && fe.Status == 404 {
			c.unmatched++
			if len(c.sample) < 200 {
				c.sample = append(c.sample, rec.Label)
			}
			return s.keepUnmatched(ctx, userID, runID, rec)
		}
		return err
	}
	return s.applyTo(ctx, userID, runID, source, item, rec, shows, c)
}

// keepUnmatched stores the whole record so it can be matched by hand. A rerun of the
// same run replaces rather than duplicates it.
func (s Store) keepUnmatched(ctx context.Context, userID int64, runID string, rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	label := rec.Label
	if len([]rune(label)) > 500 {
		label = string([]rune(label)[:500])
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO import_unmatched(run_id,profile_id,label,kind,record)
		SELECT $1::uuid,$2,$3,$4,$5 WHERE NOT EXISTS(SELECT 1 FROM import_unmatched WHERE run_id=$1::uuid AND record=$5::jsonb)`, runID, userID, label, rec.Kind, data)
	return err
}

func (s Store) applyTo(ctx context.Context, userID int64, runID, source string, item catalog.Item, rec Record, shows map[int64]map[[2]int]int64, c *counters) error {
	in, err := s.importItem(ctx, runID, item, rec, shows)
	if err != nil {
		return err
	}
	out, err := s.Library.ApplyImport(ctx, userID, source, in)
	if err != nil {
		return err
	}
	c.plays += out.PlaysAdded
	if out.Changed {
		c.imported++
	} else {
		c.unchanged++
	}
	return nil
}

// importItem resolves provider data before opening the library transaction.
func (s Store) importItem(ctx context.Context, runID string, item catalog.Item, rec Record, shows map[int64]map[[2]int]int64) (library.ImportItem, error) {
	var err error
	in := library.ImportItem{RunID: runID, TitleID: item.ID, Status: rec.Status, Progress: rec.Progress, Rating: rec.Rating, Notes: rec.Notes, ScoredAt: rec.ScoredAt, Lists: rec.Lists, Copies: rec.Copies}
	in.Copies = append([]library.ImportCopy(nil), rec.Copies...)
	for i := range in.Copies {
		if in.Copies[i].ChildID != "" {
			id, e := s.Catalog.ChildByProviderID(ctx, item.ID, in.Copies[i].ChildID)
			if e != nil && !errors.Is(e, catalog.ErrNoMatch) {
				return in, e
			}
			if id > 0 {
				in.Copies[i].CatalogID = &id
			}
		}
	}
	if f := rec.Fraction; f != nil && *f > 0 && *f <= 1 && item.Total != nil && in.Progress == nil {
		if *f >= 0.995 {
			in.Status, in.Progress = "completed", item.Total
		} else {
			in.Progress = intp(int(math.Floor(*f * float64(*item.Total))))
		}
	}
	for _, p := range rec.Plays {
		target := item.ID
		switch {
		case p.Season != nil && p.Episode != nil:
			m, ok := shows[item.ID]
			if !ok {
				if m, err = s.Catalog.EpisodeMap(ctx, item.ID); err != nil {
					return in, err
				}
				shows[item.ID] = m
			}
			id, found := m[[2]int{*p.Season, *p.Episode}]
			if !found {
				continue // The provider does not list this episode; the rest still apply.
			}
			target = id
		case p.ChildID != "" || p.ChildTitle != "" || p.TrackNumber != nil:
			var found int64
			err := error(catalog.ErrNoMatch)
			if p.ChildID != "" {
				found, err = s.Catalog.ChildByProviderID(ctx, item.ID, p.ChildID)
			}
			if found == 0 && p.ChildTitle != "" {
				found, err = s.Catalog.ChildByTitle(ctx, item.ID, p.ChildTitle)
			}
			if found == 0 && p.TrackNumber != nil && item.Kind == "music" {
				found, err = s.Catalog.ChildTrack(ctx, item.ID, *p.TrackNumber, p.ChildTitle)
			}
			switch {
			case err == nil && found > 0:
				target = found
			case item.Kind == "music":
				// A track missing from the album's listing still counts as a listen of the album.
			default:
				continue
			}
		}
		in.Plays = append(in.Plays, library.ImportPlay{TargetID: target, At: p.At, ExternalID: p.ExternalID, Minutes: p.Minutes, Details: p.Details})
	}
	return in, nil
}

func (s Store) progress(ctx context.Context, id string, c *counters) error {
	sample, _ := json.Marshal(c.sample)
	_, err := s.Pool.Exec(ctx, `UPDATE import_runs SET processed=$2,imported=$3,plays_added=$4,unchanged=$5,unmatched=$6,failed=$7,unmatched_sample=$8 WHERE id=$1`,
		id, c.processed, c.imported, c.plays, c.unchanged, c.unmatched, c.failed, sample)
	return err
}

// finish records the outcome and drops the uploaded file: it is not kept after a run.
func (s Store) finish(ctx context.Context, id, status, message string, c *counters) error {
	if c != nil {
		if err := s.progress(ctx, id, c); err != nil {
			return err
		}
	}
	if len(message) > 500 {
		message = message[:500]
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "UPDATE import_runs SET status=$2,error=$3,finished_at=clock_timestamp(),upload=CASE WHEN $2 IN ('failed','partial_success') THEN upload ELSE NULL END WHERE id=$1", id, status, message)
	if err != nil {
		return fmt.Errorf("finish import: %w", err)
	}
	if status == "succeeded" {
		if err = reconcileOwned(ctx, tx, id); err != nil {
			return err
		}
	}
	if status == "succeeded" || status == "cancelled" {
		if _, err = tx.Exec(ctx, "DELETE FROM import_batches WHERE run_id=$1", id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var errPrivateNet = netguard.ErrPrivate

func guarded(allowPrivate bool) *http.Client { return netguard.Client(allowPrivate, 60*time.Second) }

var errLinkedRun = errors.New("linked run")

// parseLinked fetches a linked service's data for its owner and saves the refreshed
// credentials and sync position once the fetch succeeds.
func (s Store) parseLinked(ctx context.Context, runID string, userID int64, optionsRaw []byte) ([]Record, error) {
	if !s.Box.Available() {
		return nil, fmt.Errorf("%w: linked services need SECRET_KEY on the server", ErrFormat)
	}
	var o struct {
		LinkID int64 `json:"link_id"`
	}
	_ = json.Unmarshal(optionsRaw, &o)
	var saved []byte
	err := s.Pool.QueryRow(ctx, `SELECT b.records FROM import_batches b JOIN service_links l ON l.id=b.link_id AND l.generation=b.generation JOIN tracking_profiles tracking ON tracking.id=b.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE b.run_id=$1 AND b.profile_id=$2 AND u.active`, runID, userID).Scan(&saved)
	if err == nil {
		var records []Record
		err = json.Unmarshal(saved, &records)
		return records, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var stale bool
	if err = s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM import_batches WHERE run_id=$1)", runID).Scan(&stale); err != nil {
		return nil, err
	}
	if stale {
		return nil, fmt.Errorf("%w: the linked account changed; start a new sync", ErrFormat)
	}
	kind, cfg, state, allow, err := s.loadLink(ctx, userID, o.LinkID)
	if err != nil {
		return nil, err
	}
	records, err := s.fetchRemote(ctx, kind, &cfg, state, allow)
	health := "healthy"
	switch {
	case errors.Is(err, ErrCredentials):
		health = "credentials_rejected"
	case errors.Is(err, ErrRateLimited):
		health = "rate_limited"
	case errors.Is(err, provider.ErrNotConfigured):
		health = "not_configured"
	case errors.Is(err, ErrFormat) || errors.Is(err, provider.ErrInvalidPayload):
		health = "invalid_response"
	case err != nil:
		health = "unavailable"
	}
	if _, healthErr := s.Pool.Exec(ctx, "UPDATE service_links SET health=$4,checked_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 AND generation=$3", userID, o.LinkID, cfg.Generation, health); healthErr != nil {
		return nil, healthErr
	}
	if err != nil {
		return nil, err
	}
	if err = s.stageLinked(ctx, runID, userID, o.LinkID, cfg, state, records); err != nil {
		return nil, err
	}
	return records, nil
}
