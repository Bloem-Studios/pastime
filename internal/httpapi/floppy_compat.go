package httpapi

// Compatibility adapter for the Silo/Bloem watch-provider plugin
// (Silo-Server/silo-plugin-watchprovider-floppy). It implements only the four
// operations that plugin calls, characterized from its request and response fields,
// on top of the native library services. Native routes never pass through it.

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/imports"
)

func (s *Server) compatRoutes() {
	r := s.Router
	r.Get("/apis/listenbrainz/1/validate-token", s.compatValidateToken)
	r.With(s.compatAuth("library:write")).Post("/apis/listenbrainz/1/submit-listens", s.compatSubmitListens)
	r.With(s.compatAuth("library:read")).Get("/api/v1/history/", s.compatHistory)
	r.With(s.compatAuth("library:read")).Get("/api/v1/playback/progress/", s.compatProgress)
	r.With(s.compatAuth("library:write")).Post("/api/v1/scrobble/", s.compatScrobble)
}

// compatToken accepts "Bearer <token>" or "Token <token>" with a native personal token.
func compatToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	for _, scheme := range []string{"Bearer ", "Token "} {
		if raw, ok := strings.CutPrefix(h, scheme); ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}

func (s *Server) compatAuth(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.accounts == nil {
				s.problem(w, r, fault.Unavailable)
				return
			}
			a, err := s.accounts.Authenticate(r.Context(), compatToken(r), "token")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !a.Allows(scope) {
				s.problem(w, r, fault.Forbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, a)))
		})
	}
}

func (s *Server) compatValidateToken(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return
	}
	if err := s.accounts.Limit(r.Context(), "compat-validate:"+remoteBucket(r), 60, time.Minute); err != nil {
		s.problem(w, r, err)
		return
	}
	a, err := s.accounts.Authenticate(r.Context(), compatToken(r), "token")
	if err != nil || !a.Allows("library:read") || !a.Allows("library:write") {
		respond(w, 200, map[string]any{"code": 200, "message": "Token invalid.", "valid": false})
		return
	}
	respond(w, 200, map[string]any{"code": 200, "message": "Token valid.", "valid": true, "user_name": a.User.Username})
}

func compatInt(r *http.Request, name string, fallback, min, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fault.Invalid(name + " is out of range.")
	}
	return n, nil
}

func compatPagination(r *http.Request, total, limit, offset int) map[string]any {
	next := ""
	if offset+limit < total {
		u := *r.URL
		q := u.Query()
		q.Set("offset", strconv.Itoa(offset+limit))
		u.RawQuery = q.Encode()
		next = u.RequestURI()
	}
	return map[string]any{"total": total, "limit": limit, "offset": offset, "next": next}
}

// externalIDs renders catalog identifiers in the plugin's "<source>_id" form, adding the
// record's own provider id so a TMDB record always carries tmdb_id.
func externalIDs(provider, providerID string, ids map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range ids {
		if s, ok := v.(string); ok && (k == "imdb" || k == "tvdb" || k == "tmdb") && s != "" {
			out[k+"_id"] = s
		}
	}
	if provider == "tmdb" && providerID != "" {
		out["tmdb_id"] = providerID
	}
	return out
}

// compatIdentity returns the source, id, title and identifiers a client matches on: the
// show's for an episode, the item's own otherwise.
func compatIdentity(kind, provider, providerID, title string, ids map[string]any, showProvider, showID, showTitle string, showIDs map[string]any) (string, string, string, map[string]string) {
	if kind == "episode" && showProvider != "" {
		return showProvider, showID, showTitle, externalIDs(showProvider, showID, showIDs)
	}
	return provider, providerID, title, externalIDs(provider, providerID, ids)
}

func compatDate(r *http.Request, name string) (*time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fault.Invalid(name + " must be YYYY-MM-DD.")
	}
	return &t, nil
}

func (s *Server) compatHistory(w http.ResponseWriter, r *http.Request) {
	limit, err := compatInt(r, "limit", 50, 1, 200)
	if err == nil {
		var offset int
		if offset, err = compatInt(r, "offset", 0, 0, 1000000); err == nil {
			var start, end *time.Time
			if start, err = compatDate(r, "start_date"); err == nil {
				if end, err = compatDate(r, "end_date"); err == nil {
					s.compatHistoryPage(w, r, start, end, limit, offset)
					return
				}
			}
		}
	}
	s.problem(w, r, err)
}

func (s *Server) compatHistoryPage(w http.ResponseWriter, r *http.Request, start, end *time.Time, limit, offset int) {
	records, total, err := s.library.PlayRecords(r.Context(), actor(r), start, end, limit, offset)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	type day struct {
		Date    string           `json:"date"`
		Entries []map[string]any `json:"entries"`
	}
	days := []day{}
	for _, p := range records {
		source, mediaID, itemTitle, ids := compatIdentity(p.Kind, p.Provider, p.ProviderID, p.Title, p.ExternalIDs, p.ShowProvider, p.ShowProviderID, p.ShowTitle, p.ShowExternalIDs)
		display, code, label := p.Title, "", ""
		if p.Kind == "episode" && p.SeasonNumber != nil && p.EpisodeNumber != nil {
			code = "S" + pad(*p.SeasonNumber) + "E" + pad(*p.EpisodeNumber)
			label = p.Title
			display = p.ShowTitle + " " + code
		}
		entry := map[string]any{
			"media_type": p.Kind, "title": p.Title, "display_title": display, "status": "Completed",
			"played_at_local": p.PlayedAt.UTC().Format(time.RFC3339Nano), "play_count": 1, "instance_id": strconv.FormatInt(p.PlayID, 10),
			"episode_label": label, "episode_code": code,
			"item": map[string]any{"media_type": p.Kind, "media_id": mediaID, "source": source, "title": itemTitle,
				"season_number": p.SeasonNumber, "episode_number": p.EpisodeNumber, "provider_external_ids": ids},
		}
		date := p.PlayedAt.UTC().Format("2006-01-02")
		if len(days) == 0 || days[len(days)-1].Date != date {
			days = append(days, day{Date: date})
		}
		days[len(days)-1].Entries = append(days[len(days)-1].Entries, entry)
	}
	respond(w, 200, map[string]any{"pagination": compatPagination(r, total, limit, offset), "results": days})
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func (s *Server) compatProgress(w http.ResponseWriter, r *http.Request) {
	limit, err := compatInt(r, "limit", 50, 1, 200)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	offset, err := compatInt(r, "offset", 0, 0, 1000000)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	kinds := []string{"movie", "episode"}
	if raw := r.URL.Query().Get("media_type"); raw != "" {
		kinds = nil
		for _, k := range strings.Split(raw, ",") {
			if k = strings.TrimSpace(k); k == "movie" || k == "episode" {
				kinds = append(kinds, k)
			}
		}
	}
	var since *time.Time
	if raw := r.URL.Query().Get("updated_since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			s.problem(w, r, fault.Invalid("updated_since must be RFC 3339."))
			return
		}
		since = &t
	}
	// Stored positions are unfinished by definition; completed=true has no rows.
	if strings.EqualFold(r.URL.Query().Get("completed"), "true") {
		kinds = nil
	}
	resumes, total, err := s.library.Resumes(r.Context(), actor(r), since, kinds, limit, offset)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	results := make([]map[string]any, 0, len(resumes))
	for _, p := range resumes {
		source, mediaID, _, ids := compatIdentity(p.Kind, p.Provider, p.ProviderID, p.Title, p.ExternalIDs, p.ShowProvider, p.ShowProviderID, p.ShowTitle, p.ShowExternalIDs)
		duration := 0
		if p.DurationSeconds != nil {
			duration = *p.DurationSeconds
		}
		results = append(results, map[string]any{
			"media_type": p.Kind, "source": source, "media_id": mediaID, "season_number": p.SeasonNumber, "episode_number": p.EpisodeNumber,
			"ids": ids, "title": p.Title, "series_title": p.ShowTitle, "position_seconds": p.PositionSeconds, "duration_seconds": duration,
			"completed": false, "updated_at": p.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	respond(w, 200, map[string]any{"pagination": compatPagination(r, total, limit, offset), "results": results})
}

type compatScrobbleInput struct {
	Action          string            `json:"action"`
	MediaType       string            `json:"media_type"`
	IDs             map[string]string `json:"ids"`
	Title           string            `json:"title"`
	SeriesTitle     string            `json:"series_title"`
	SeasonNumber    *int              `json:"season_number"`
	EpisodeNumber   *int              `json:"episode_number"`
	PositionSeconds *float64          `json:"position_seconds"`
	DurationSeconds *float64          `json:"duration_seconds"`
	Completed       *bool             `json:"completed"`
	PlayedAt        string            `json:"played_at"`
}

// compatScrobble matches the reported item synchronously (the plugin waits and maps
// failures to retryable faults), then records a play on completion or a resume
// position otherwise.
func (s *Server) compatScrobble(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		s.problem(w, r, fault.Unavailable)
		return
	}
	// Unknown fields are ignored so newer plugin versions keep working.
	var in compatScrobbleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		s.problem(w, r, fault.Invalid("Send a JSON scrobble object."))
		return
	}
	if in.Action != "start" && in.Action != "pause" && in.Action != "stop" {
		s.problem(w, r, fault.Invalid("action must be start, pause or stop."))
		return
	}
	ids := map[string]string{}
	for k, v := range in.IDs {
		k = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(k)), "_id")
		if v = strings.TrimSpace(v); v != "" && len(v) <= 40 && (k == "tmdb" || k == "imdb" || k == "tvdb") {
			ids[k] = v
		}
	}
	if len(ids) == 0 {
		s.problem(w, r, fault.Invalid("Provide a TMDB, IMDb or TVDB identifier."))
		return
	}
	ref := catalog.Reference{Kind: in.MediaType, Title: in.Title}
	switch in.MediaType {
	case "movie":
		ref.TMDB, ref.IMDB, ref.TVDB = ids["tmdb"], ids["imdb"], ids["tvdb"]
	case "episode":
		if in.SeasonNumber == nil || in.EpisodeNumber == nil || *in.SeasonNumber < 0 || *in.EpisodeNumber < 1 {
			s.problem(w, r, fault.Invalid("Episodes need season and episode numbers."))
			return
		}
		ref.ShowTMDB, ref.ShowIMDB, ref.ShowTVDB, ref.ShowTitle = ids["tmdb"], ids["imdb"], ids["tvdb"], in.SeriesTitle
		ref.Season, ref.Episode = in.SeasonNumber, in.EpisodeNumber
	default:
		s.problem(w, r, fault.Invalid("media_type must be movie or episode."))
		return
	}
	at := time.Now().UTC()
	if in.PlayedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, in.PlayedAt)
		if err != nil || t.After(at.Add(5*time.Minute)) || t.Year() < 1900 {
			s.problem(w, r, fault.Invalid("played_at must be an RFC 3339 time that is not in the future."))
			return
		}
		at = t.UTC()
	}
	if !s.accountLimit(w, r, "compat-scrobble", 120) {
		return
	}
	m, err := s.catalog.MatchVideo(r.Context(), ref)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	target := m.Title.ID
	if m.Episode != nil {
		target = m.Episode.ID
	}
	a := actor(r)
	position, duration := seconds(in.PositionSeconds), seconds(in.DurationSeconds)
	completed := in.Action == "stop" && in.Completed != nil && *in.Completed
	// Without an explicit flag, a stop within the last tenth of the runtime completes it.
	if in.Action == "stop" && in.Completed == nil && position != nil && duration != nil && *position >= *duration*9/10 {
		completed = true
	}
	if completed {
		if _, err = s.library.ScrobbleAs(r.Context(), a, "watch_sync", m.Title.ID, target, at); err != nil {
			s.problem(w, r, err)
			return
		}
		if err = s.library.ClearProgress(r.Context(), a, target); err != nil {
			s.problem(w, r, err)
			return
		}
	} else if position != nil {
		if err = s.library.SaveProgress(r.Context(), a, target, *position, duration, "watch_sync"); err != nil {
			s.problem(w, r, err)
			return
		}
	}
	respond(w, 200, map[string]any{"action": in.Action, "completed": completed, "media_type": in.MediaType})
}

func seconds(v *float64) *int {
	if v == nil || *v < 0 || math.IsNaN(*v) || *v > 10000000 {
		return nil
	}
	n := int(math.Round(*v))
	return &n
}

// compatSubmitListens accepts ListenBrainz submissions, so scrobblers that speak the
// ListenBrainz API can point at this server. Listens are recorded as album plays.
func (s *Server) compatSubmitListens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		s.problem(w, r, fault.Invalid("The submission is larger than 4 MiB."))
		return
	}
	listens, err := imports.ParseListens(body, time.Now())
	if err == nil {
		err = s.imports.QueueListens(r.Context(), actor(r).TrackingID(), listens)
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "ok"})
}
