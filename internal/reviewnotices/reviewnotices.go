// Package reviewnotices tells the customer-notice service how each
// customer's year went, once a year, so it can email them their Year in Review.
// Pastime stores no email addresses: the service finds the customer by the sign-in
// subject of their paid access, honours their choice about such emails, and
// writes the email itself. Pastime sends only the year, the minutes and the
// number of titles.
//
// From 27 December 09:00 UTC the worker records one row per customer with
// access and activity in the year (review_notices), then sends each row once,
// retrying temporary failures hourly. Without REVIEW_NOTICES_URL and
// REVIEW_NOTICES_TOKEN nothing is sent.
package reviewnotices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxAttempts is how often a temporary failure is retried before giving up.
const maxAttempts = 5

// ErrUnauthorized means the notice service refused the token; nothing more is sent this run.
var ErrUnauthorized = errors.New("reviewnotices: the customer system refused the token")

// Sender sends the notices. URL is the service's hook
// (https://…/api/v1/hooks/trackers/<operator>/notices) and Token its bearer token.
type Sender struct {
	Pool   *pgxpool.Pool
	URL    string
	Token  string
	Client *http.Client
}

// Configured reports whether a hook and token are set.
func (s Sender) Configured() bool { return s.URL != "" && s.Token != "" }

// Due reports the year whose notices go out at now: from 27 December 09:00 UTC
// to the end of the year.
func Due(now time.Time) (int, bool) {
	now = now.UTC()
	start := time.Date(now.Year(), time.December, 27, 9, 0, 0, 0, time.UTC)
	return now.Year(), !now.Before(start)
}

// Summary is one customer's year as the notice reports it.
type Summary struct {
	AccountID int64
	Subject   string
	Minutes   int
	Titles    int
}

// recipientsSQL finds every account with paid access now, with the subject of
// its access grant (through its own sign-in or its linked Bloem account), and
// totals its profiles' year: minutes of plays and of finished sessions that
// recorded no play, and distinct titles played or read. $1 and $2 bound the year.
const recipientsSQL = `WITH recipients AS (
 SELECT DISTINCT ON (u.id) u.id AS account_id, g.subject
 FROM users u
 JOIN service_access_policy p ON p.id = 1 AND p.enabled AND p.fresh_until > clock_timestamp()
 JOIN service_access_grants g ON g.expires_at > clock_timestamp()
 WHERE EXISTS (SELECT 1 FROM oidc_identities o WHERE o.user_id = u.id AND o.issuer = p.issuer AND o.subject = g.subject)
    OR EXISTS (SELECT 1 FROM bloem_accounts a JOIN bloem_instances i ON i.id = a.instance_id AND i.active
               JOIN bloem_login_issuers l ON l.instance_id = i.id AND l.login_issuer = p.issuer AND l.subject_mode = 'user_uuid'
               WHERE a.account_id = u.id AND a.active AND a.identity_issuer = i.identity_issuer AND a.identity_subject = g.subject)
 ORDER BY u.id, g.subject
), profiles AS (
 SELECT r.account_id, t.id AS profile_id FROM recipients r JOIN tracking_profiles t ON t.account_id = r.account_id
), timed AS (
 SELECT pr.account_id, d.minutes FROM play_days d JOIN profiles pr ON pr.profile_id = d.profile_id
 WHERE d.day >= $1::timestamptz::date AND d.day < $2::timestamptz::date
 UNION ALL
 SELECT pr.account_id, COALESCE(k.minutes, 0) FROM checkins k JOIN profiles pr ON pr.profile_id = k.profile_id
 WHERE k.kind = 'session' AND k.status = 'finished' AND k.play_id IS NULL AND k.finished_at >= $1 AND k.finished_at < $2
), used AS (
 SELECT x.profile_id, x.entry_id FROM (
   SELECT profile_id, entry_id FROM plays WHERE played_at >= $1 AND played_at < $2 AND NOT marked AND profile_id IN (SELECT profile_id FROM profiles)
   UNION SELECT profile_id, entry_id FROM checkins WHERE kind = 'session' AND status = 'finished' AND finished_at >= $1 AND finished_at < $2
     AND profile_id IN (SELECT profile_id FROM profiles)
 ) x
), titles AS (
 SELECT pr.account_id, count(DISTINCT COALESCE('c' || e.catalog_item_id, 'e' || e.id)) AS n
 FROM used u JOIN profiles pr ON pr.profile_id = u.profile_id JOIN library_entries e ON e.profile_id = u.profile_id AND e.id = u.entry_id
 GROUP BY 1
), summary AS (
 SELECT r.account_id, r.subject,
  LEAST(COALESCE((SELECT sum(t.minutes) FROM timed t WHERE t.account_id = r.account_id), 0), 100000000)::int AS minutes,
  LEAST(COALESCE(ti.n, 0), 1000000)::int AS titles
 FROM recipients r LEFT JOIN titles ti ON ti.account_id = r.account_id
)
SELECT account_id, subject, minutes, titles FROM summary WHERE minutes > 0 OR titles > 0 ORDER BY account_id`

// Summaries totals the year for every customer with access and activity.
func (s Sender) Summaries(ctx context.Context, year int) ([]Summary, error) {
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.Pool.Query(ctx, recipientsSQL, from, from.AddDate(1, 0, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var x Summary
		if err := rows.Scan(&x.AccountID, &x.Subject, &x.Minutes, &x.Titles); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Result counts one run's outcomes by status.
type Result map[string]int

// Run records the year's notices when they are due and sends those waiting,
// at most limit of them.
func (s Sender) Run(ctx context.Context, now time.Time, limit int) (Result, error) {
	out := Result{}
	year, due := Due(now)
	if !due || !s.Configured() {
		return out, nil
	}
	if err := s.record(ctx, year); err != nil {
		return out, err
	}
	for range limit {
		status, sent, err := s.sendNext(ctx, year)
		if err != nil {
			return out, err
		}
		if !sent {
			break
		}
		out[status]++
	}
	return out, nil
}

// record keeps one row per customer for the year; a row already kept keeps the
// numbers it was first given.
func (s Sender) record(ctx context.Context, year int) error {
	summaries, err := s.Summaries(ctx, year)
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, x := range summaries {
		batch.Queue(`INSERT INTO review_notices(account_id,year,subject,minutes,titles) VALUES($1,$2,$3,$4,$5) ON CONFLICT (account_id,year) DO NOTHING`,
			x.AccountID, year, x.Subject, x.Minutes, x.Titles)
	}
	return s.Pool.SendBatch(ctx, batch).Close()
}

// sendNext sends one waiting notice, holding its row so concurrent workers
// never send it twice.
func (s Sender) sendNext(ctx context.Context, year int) (string, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var x Summary
	var attempts int
	err = tx.QueryRow(ctx, `SELECT account_id,subject,minutes,titles,attempts FROM review_notices
		WHERE year=$1 AND status='pending' AND (next_attempt_at IS NULL OR next_attempt_at<=clock_timestamp())
		ORDER BY account_id LIMIT 1 FOR UPDATE SKIP LOCKED`, year).Scan(&x.AccountID, &x.Subject, &x.Minutes, &x.Titles, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	status, problem := s.Post(ctx, year, x, false)
	if errors.Is(problem, ErrUnauthorized) {
		return "", false, problem
	}
	var next *time.Time
	switch {
	case status != "":
	case attempts+1 >= maxAttempts:
		status = "failed"
	default:
		status = "pending"
		retry := time.Now().Add(time.Hour)
		next = &retry
	}
	var message *string
	if problem != nil {
		m := problem.Error()
		if len(m) > 300 {
			m = m[:300]
		}
		message = &m
	}
	if _, err = tx.Exec(ctx, `UPDATE review_notices SET status=$3,attempts=attempts+1,next_attempt_at=$4,last_error=$5,updated_at=clock_timestamp()
		WHERE account_id=$1 AND year=$2`, x.AccountID, year, status, next, message); err != nil {
		return "", false, err
	}
	return status, true, tx.Commit(ctx)
}

type notice struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Year    int    `json:"year"`
	Minutes int    `json:"minutes"`
	Titles  int    `json:"titles"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

// Post sends one notice and maps the service's answer to a final status: sent,
// opted_out, unknown (no customer signs in with the subject), refused (the service
// rejected the notice) or, for a dry run, would_send. An empty status with an
// error is temporary and may be retried; ErrUnauthorized stops the run.
func (s Sender) Post(ctx context.Context, year int, x Summary, dryRun bool) (string, error) {
	body, err := json.Marshal(notice{Kind: "year_in_review", Subject: x.Subject, Year: year, Minutes: x.Minutes, Titles: x.Titles, DryRun: dryRun})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pastime-Notices/1")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("could not reach the customer system")
	}
	defer resp.Body.Close()
	var answer struct {
		Status string `json:"status"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(data, &answer)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return "unknown", nil
	case resp.StatusCode == http.StatusBadRequest:
		return "refused", fmt.Errorf("the customer system refused the notice")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", fmt.Errorf("the customer system answered %d", resp.StatusCode)
	case resp.StatusCode >= 300:
		return "refused", fmt.Errorf("the customer system answered %d", resp.StatusCode)
	}
	switch answer.Status {
	case "queued", "already_queued":
		return "sent", nil
	case "opted_out":
		return "opted_out", nil
	case "would_queue":
		return "would_send", nil
	}
	return "", fmt.Errorf("the customer system gave an unknown answer")
}

// Check posts a dry run for every customer the year would include and counts
// the answers, sending no email and recording nothing.
func (s Sender) Check(ctx context.Context, year int) (Result, error) {
	out := Result{}
	if !s.Configured() {
		return out, errors.New("set REVIEW_NOTICES_URL and REVIEW_NOTICES_TOKEN first")
	}
	summaries, err := s.Summaries(ctx, year)
	if err != nil {
		return out, err
	}
	for _, x := range summaries {
		status, err := s.Post(ctx, year, x, true)
		if errors.Is(err, ErrUnauthorized) {
			return out, err
		}
		if status == "" {
			status = "temporary_error"
		}
		out[status]++
	}
	return out, nil
}
