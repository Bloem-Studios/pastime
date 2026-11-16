package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/jobs"
)

const CalendarTitleJob = "calendar_title"

type calendarTitleInput struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Scheme   string `json:"scheme"`
	Value    string `json:"value"`
}

// CalendarTitleRequest contains public provider identities, never profile state.
type CalendarTitleRequest struct {
	Kind string
	IDs  map[string]string
}

func (s *Service) calendarTitlePayload(kind string, ids map[string]string) (json.RawMessage, bool) {
	if kind == "ebook" {
		kind = "book"
	}
	if kind == "book" || kind == "audiobook" {
		in := calendarTitleInput{Kind: kind}
		if kind == "audiobook" {
			in.Provider, in.Scheme, in.Value = "audnexus", "asin", strings.ToUpper(strings.TrimSpace(ids["asin"]))
		} else {
			in.Provider, in.Scheme, in.Value = "openlibrary", "isbn", strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(ids["isbn"]), "-", ""), " ", ""))
		}
		if !validCalendarBook(in) {
			return nil, false
		}
		if p, ok := s.Providers[in.Provider]; !ok || !supports(p, kind) {
			return nil, false
		}
		payload, _ := json.Marshal(in)
		return payload, true
	}
	if kind != "movie" && kind != "series" {
		return nil, false
	}
	in := calendarTitleInput{Kind: kind}
	for _, scheme := range []string{"tmdb", "tvdb", "imdb"} {
		value := ids[scheme]
		if value == "" || len(value) > 200 {
			continue
		}
		if p, ok := s.Providers[scheme]; ok && supports(p, kind) {
			in.Provider = scheme
			in.Scheme = scheme
			in.Value = value
			break
		}
		if p, ok := s.Providers["tmdb"]; ok && supports(p, kind) {
			if _, ok := p.(provider.ExternalFinder); ok {
				in.Provider = "tmdb"
				in.Scheme = scheme
				in.Value = value
				break
			}
		}
	}
	if in.Value == "" {
		return nil, false
	}
	payload, _ := json.Marshal(in)
	return payload, true
}

// EnqueueCalendarTitles performs one indexed query for existing jobs, then queues
// at most 50 missing identities. Previously failed/done lookups cannot starve later
// titles. Daily keys bound retries without calling providers in HTTP requests.
func (s *Service) EnqueueCalendarTitles(ctx context.Context, requests []CalendarTitleRequest, now time.Time) (int, error) {
	keys := []string{}
	payloads := map[string]json.RawMessage{}
	for _, r := range requests {
		payload, ok := s.calendarTitlePayload(r.Kind, r.IDs)
		if !ok {
			continue
		}
		sum := sha256.Sum256(payload)
		key := CalendarTitleJob + ":" + hex.EncodeToString(sum[:]) + ":" + now.UTC().Format("2006-01-02")
		if _, ok := payloads[key]; !ok {
			keys = append(keys, key)
			payloads[key] = payload
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT dedupe_key FROM jobs WHERE dedupe_key=ANY($1::text[])`, keys)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return 0, err
		}
		delete(payloads, key)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	added := 0
	for _, key := range keys {
		payload, ok := payloads[key]
		if !ok {
			continue
		}
		if added >= 50 {
			break
		}
		if _, err = jobs.Enqueue(ctx, s.Pool, CalendarTitleJob, key, "idempotent", payload, 3); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

func (s *Service) CalendarTitleHandler(ctx context.Context, payload json.RawMessage) error {
	var in calendarTitleInput
	if json.Unmarshal(payload, &in) != nil {
		return jobs.ErrInvalidPayload
	}
	if in.Kind == "book" || in.Kind == "audiobook" {
		if !validCalendarBook(in) {
			return jobs.ErrInvalidPayload
		}
		return s.calendarBook(ctx, in)
	}
	if (in.Kind != "movie" && in.Kind != "series") || in.Value == "" || len(in.Value) > 200 || (in.Scheme != "tmdb" && in.Scheme != "tvdb" && in.Scheme != "imdb") || (in.Provider != "tmdb" && in.Provider != "tvdb") {
		return jobs.ErrInvalidPayload
	}
	var err error
	if in.Provider == in.Scheme {
		_, err = s.Resolve(ctx, in.Provider, in.Kind, in.Value)
	} else {
		_, err = s.ResolveExternal(ctx, in.Provider, in.Kind, in.Scheme, in.Value)
	}
	if errors.Is(err, ErrNoMatch) || errors.Is(err, provider.ErrNotConfigured) {
		return nil
	}
	return err
}

// validCalendarBook restricts jobs to public exact identifiers and fixed adapters.
func validCalendarBook(in calendarTitleInput) bool {
	if in.Kind == "audiobook" {
		return in.Provider == "audnexus" && in.Scheme == "asin" && len(in.Value) == 10 && strings.Trim(in.Value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == ""
	}
	if in.Kind != "book" || in.Provider != "openlibrary" || in.Scheme != "isbn" || (len(in.Value) != 10 && len(in.Value) != 13) {
		return false
	}
	return strings.Trim(in.Value, "0123456789X") == "" && !strings.Contains(in.Value[:len(in.Value)-1], "X") && (len(in.Value) != 13 || !strings.Contains(in.Value, "X"))
}

func (s *Service) calendarBook(ctx context.Context, in calendarTitleInput) error {
	p, ok := s.Providers[in.Provider]
	if !ok || !supports(p, in.Kind) {
		return nil
	}
	// Replay does not refetch usable artwork; multiple exact local records remain
	// ambiguous rather than selecting an arbitrary title.
	var count int
	var artwork bool
	err := s.Pool.QueryRow(ctx, `SELECT count(*),coalesce(bool_or(image_url<>''),false) FROM catalog_items WHERE kind=$1 AND parent_id IS NULL AND external_ids @> jsonb_build_object($2::text,$3::text)`, in.Kind, in.Scheme, in.Value).Scan(&count, &artwork)
	if err != nil {
		return err
	}
	if count > 1 || (count == 1 && artwork) {
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	defer cancel()
	var rec provider.Record
	if in.Kind == "book" {
		isbnProvider, ok := p.(interface {
			FetchISBN(context.Context, string, string) (provider.Record, error)
		})
		if !ok {
			return nil
		}
		rec, err = isbnProvider.FetchISBN(fetchCtx, in.Kind, in.Value)
	} else {
		rec, err = p.Fetch(fetchCtx, in.Kind, in.Value)
	}
	if errors.Is(err, provider.ErrNotFound) || errors.Is(err, provider.ErrNotConfigured) {
		return nil
	}
	if err != nil {
		return err
	}
	if rec.Provider != in.Provider || rec.Kind != in.Kind || rec.ExternalIDs[in.Scheme] != in.Value || len(rec.Children) > 0 {
		return provider.ErrInvalidPayload
	}
	rec, err = provider.Normalize(rec)
	if err != nil {
		return err
	}
	// A lookup that supplies no artwork is not useful for this metadata job.
	if rec.ImageURL == "" {
		return nil
	}
	// Conflict handling is atomic: an ISBN of another edition on the same OL
	// work is never replaced. Existing descriptive metadata is preserved.
	if rec.Credits == nil {
		rec.Credits = []string{}
	}
	if rec.Genres == nil {
		rec.Genres = []string{}
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO catalog_items(kind,provider,provider_id,title,image_url,unit,external_ids,credits,genres,release_date,total,provider_rating)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
 ON CONFLICT(provider,kind,provider_id) DO UPDATE SET image_url=EXCLUDED.image_url
 WHERE catalog_items.external_ids @> EXCLUDED.external_ids AND catalog_items.image_url=''`, rec.Kind, rec.Provider, rec.ProviderID, rec.Title, rec.ImageURL, rec.Unit, rec.ExternalIDs, rec.Credits, rec.Genres, rec.ReleaseDate, rec.Total, rec.Rating)
	return err
}
