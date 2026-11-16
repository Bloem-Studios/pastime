package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/jackc/pgx/v5"
)

const CriticJob = "critic_refresh"

type CriticScore struct {
	ExternalID  *int64     `json:"external_id"`
	Score       *float64   `json:"score"`
	Recommended *float64   `json:"recommended"`
	Tier        string     `json:"tier"`
	Status      string     `json:"status"`
	CheckedAt   *time.Time `json:"checked_at"`
}

// Critic returns only durable cached data. Interactive requests never call OpenCritic.
func (s *Service) Critic(ctx context.Context, id int64) (CriticScore, error) {
	out := CriticScore{Status: "not_configured"}
	var kind string
	if err := s.Pool.QueryRow(ctx, "SELECT kind FROM catalog_items WHERE id=$1", id).Scan(&kind); err != nil {
		if err == pgx.ErrNoRows {
			return out, fault.NotFound
		}
		return out, err
	}
	if kind != "game" {
		return out, fault.NotFound
	}
	if s.OpenCriticKey == "" {
		return out, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO critic_scores(catalog_item_id) VALUES($1) ON CONFLICT DO NOTHING", id); err != nil {
		return out, err
	}
	var due bool
	if err = tx.QueryRow(ctx, "SELECT external_id,score,recommended,tier,status,checked_at,refresh_after<=clock_timestamp() FROM critic_scores WHERE catalog_item_id=$1 FOR UPDATE", id).Scan(&out.ExternalID, &out.Score, &out.Recommended, &out.Tier, &out.Status, &out.CheckedAt, &due); err != nil {
		return out, err
	}
	if due {
		payload, _ := json.Marshal(map[string]string{"catalog_id": strconv.FormatInt(id, 10)})
		if _, err = jobs.Enqueue(ctx, tx, CriticJob, "critic:"+strconv.FormatInt(id, 10)+":"+time.Now().UTC().Format("2006-01-02-15"), "idempotent", payload, 3); err != nil {
			return out, err
		}
		if _, err = tx.Exec(ctx, "UPDATE critic_scores SET refresh_after=clock_timestamp()+interval '1 hour' WHERE catalog_item_id=$1", id); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

var errCriticQuota = errors.New("critic quota unavailable")
var errCriticCredentials = errors.New("critic credentials rejected")

func (s *Service) criticRequest(ctx context.Context, path string, out any) error {
	// A conservative hard budget of 100 requests per calendar month is shared by
	// all workers and includes failed requests. Operators can inspect the counter.
	var granted bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO critic_quota(provider,window_start,used) VALUES('opencritic',date_trunc('month',clock_timestamp() AT TIME ZONE 'UTC')::date,1)
 ON CONFLICT(provider) DO UPDATE SET window_start=excluded.window_start,
 used=CASE WHEN critic_quota.window_start=excluded.window_start THEN critic_quota.used+1 ELSE 1 END
 WHERE critic_quota.next_allowed<=clock_timestamp() AND (critic_quota.window_start<>excluded.window_start OR critic_quota.used<100) RETURNING true`).Scan(&granted)
	if err == pgx.ErrNoRows {
		return errCriticQuota
	}
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://opencritic-api.p.rapidapi.com"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-RapidAPI-Key", s.OpenCriticKey)
	req.Header.Set("X-RapidAPI-Host", "opencritic-api.p.rapidapi.com")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if s.CriticHTTP != nil {
		client = s.CriticHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("critic unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		delay := time.Hour
		if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 {
			delay = time.Duration(n) * time.Second
		} else if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil && at.After(time.Now()) {
			delay = time.Until(at)
		}
		if delay > 30*24*time.Hour {
			delay = 30 * 24 * time.Hour
		}
		_, e := s.Pool.Exec(ctx, "UPDATE critic_quota SET next_allowed=GREATEST(next_allowed,clock_timestamp()+make_interval(secs=>$1)) WHERE provider='opencritic'", delay.Seconds())
		if e != nil {
			return e
		}
		return errCriticQuota
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		if _, err := s.Pool.Exec(ctx, "UPDATE critic_quota SET next_allowed=GREATEST(next_allowed,clock_timestamp()+interval '1 day') WHERE provider='opencritic'"); err != nil {
			return err
		}
		return errCriticCredentials
	}
	if resp.StatusCode != 200 {
		return errors.New("critic unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return errors.New("invalid critic response")
	}
	return json.Unmarshal(data, out)
}
func (s *Service) CriticHandler(ctx context.Context, raw json.RawMessage) error {
	var p struct {
		ID string `json:"catalog_id"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return jobs.ErrInvalidPayload
	}
	id, err := strconv.ParseInt(p.ID, 10, 64)
	if err != nil || id < 1 {
		return jobs.ErrInvalidPayload
	}
	if s.OpenCriticKey == "" {
		return nil
	}
	var title, kind string
	var year *int
	err = s.Pool.QueryRow(ctx, "SELECT title,kind,extract(year FROM release_date)::int FROM catalog_items WHERE id=$1", id).Scan(&title, &kind, &year)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if kind != "game" {
		return nil
	}
	status := "unavailable"
	after := time.Hour
	var score CriticScore
	var matches []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		ReleaseDate string `json:"firstReleaseDate"`
	}
	err = s.criticRequest(ctx, "/game/search?criteria="+url.QueryEscape(title), &matches)
	var matched int64
	if err == nil {
		status = "no_match"
		after = 7 * 24 * time.Hour
		for _, m := range matches {
			if normalizeTitle(m.Name) != normalizeTitle(title) {
				continue
			}
			if year != nil && m.ReleaseDate != "" && (len(m.ReleaseDate) < 4 || m.ReleaseDate[:4] != strconv.Itoa(*year)) {
				continue
			}
			if matched != 0 {
				matched = 0
				status = "ambiguous"
				break
			}
			matched = m.ID
		}
		if matched > 0 {
			var detail struct {
				ID          int64    `json:"id"`
				Score       *float64 `json:"topCriticScore"`
				Recommended *float64 `json:"percentRecommended"`
				Tier        string   `json:"tier"`
			}
			err = s.criticRequest(ctx, "/game/"+strconv.FormatInt(matched, 10), &detail)
			if err == nil && detail.ID == matched && (detail.Score == nil || *detail.Score >= 0 && *detail.Score <= 100) && (detail.Recommended == nil || *detail.Recommended >= 0 && *detail.Recommended <= 100) {
				score = CriticScore{ExternalID: &matched, Score: detail.Score, Recommended: detail.Recommended, Tier: detail.Tier}
				status = "available"
			} else if err == nil {
				err = errors.New("invalid critic response")
			}
		}
	}
	if err != nil {
		status = "unavailable"
		after = time.Hour
		if errors.Is(err, errCriticQuota) {
			status = "quota_limited"
			after = 24 * time.Hour
		}
		if errors.Is(err, errCriticCredentials) {
			status = "credentials_rejected"
			after = 24 * time.Hour
		}
	}
	_, err = s.Pool.Exec(ctx, `UPDATE critic_scores SET external_id=COALESCE($2,external_id),score=CASE WHEN $6='available' THEN $3 ELSE score END,recommended=CASE WHEN $6='available' THEN $4 ELSE recommended END,tier=CASE WHEN $6='available' THEN $5 ELSE tier END,status=$6,checked_at=clock_timestamp(),refresh_after=clock_timestamp()+make_interval(secs=>$7) WHERE catalog_item_id=$1`, id, score.ExternalID, score.Score, score.Recommended, score.Tier, status, after.Seconds())
	return err
}
