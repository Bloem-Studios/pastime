package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

func (s *Server) insightRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/home", s.home)
		r.Get("/api/v1/history", s.history)
		r.Get("/api/v1/stats", s.stats)
		r.Get("/api/v1/stats/years", s.statsYears)
		r.Get("/api/v1/review/{year}", s.review)
		r.Get("/api/v1/calendar", s.calendar)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Delete("/api/v1/history/{playID}", s.deleteHistory)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/calendar-feed", s.feedStatus)
		r.Post("/api/v1/me/calendar-feed", s.rotateFeed)
		r.Delete("/api/v1/me/calendar-feed", s.revokeFeed)
	})
	r := s.Router
	r.Get("/calendar/{secret}.ics", s.calendarFeed)
}

// dateParam accepts YYYY-MM-DD (UTC midnight) or an RFC 3339 timestamp.
func dateParam(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fault.Invalid("Dates use YYYY-MM-DD or RFC 3339.")
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	out, err := s.library.Home(r.Context(), actor(r), now)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	upcoming, err := s.mergeCalendarSources(r.Context(), actor(r), utcDay(now), utcDay(now).AddDate(0, 0, 14), "all", out.Upcoming)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if len(upcoming) > 10 {
		upcoming = upcoming[:10]
	}
	respond(w, 200, struct {
		library.Home
		Upcoming []calendarRelease `json:"upcoming"`
	}{out, upcoming})
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	f := library.HistoryFilter{MediaType: r.URL.Query().Get("media_type"), Cursor: r.URL.Query().Get("cursor"), Limit: limit}
	if f.EntryID, err = optionalID(r.URL.Query().Get("entry_id")); err != nil {
		s.problem(w, r, err)
		return
	}
	for name, target := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if r.URL.Query().Get(name) == "" {
			continue
		}
		t, err := dateParam(r, name, time.Time{})
		if err != nil {
			s.problem(w, r, err)
			return
		}
		*target = &t
	}
	out, err := s.library.History(r.Context(), actor(r), f)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) deleteHistory(w http.ResponseWriter, r *http.Request) {
	id, err := identity.ParseID(chi.URLParam(r, "playID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.DeletePlay(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	to, err := dateParam(r, "to", utcDay(time.Now()).AddDate(0, 0, 1))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	from, err := dateParam(r, "from", to.AddDate(-1, 0, 0))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Insights(r.Context(), actor(r), from, to)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) statsYears(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.Years(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": out})
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "year")
	year, err := strconv.Atoi(raw)
	if err != nil || len(raw) != 4 {
		s.problem(w, r, fault.Invalid("Choose a year from 1900 up to this one."))
		return
	}
	out, err := s.library.Review(r.Context(), actor(r), year, time.Now())
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	from, err := dateParam(r, "from", utcDay(time.Now()))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	to, err := dateParam(r, "to", from.AddDate(0, 0, 30))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.mergedCalendar(r.Context(), actor(r), utcDay(from), utcDay(to), r.URL.Query().Get("filter"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	respond(w, 200, map[string]any{"items": out})
}

type feedStatus struct {
	Enabled   bool       `json:"enabled"`
	CreatedAt *time.Time `json:"created_at"`
}

func (s *Server) feedStatus(w http.ResponseWriter, r *http.Request) {
	created, err := s.accounts.FeedStatus(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, feedStatus{Enabled: created != nil, CreatedAt: created})
}

func (s *Server) rotateFeed(w http.ResponseWriter, r *http.Request) {
	if !s.accountLimit(w, r, "calendar-feed", 10) {
		return
	}
	raw, err := s.accounts.RotateFeed(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]string{"url": s.origin + "/calendar/" + raw + ".ics"})
}

func (s *Server) revokeFeed(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.RevokeFeed(r.Context(), actor(r)); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// calendarFeed serves an iCalendar document for subscription clients. The secret is the
// only credential; it grants read access to release dates of followed titles and nothing else.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return
	}
	if err := s.accounts.Limit(r.Context(), "calendar-feed:"+remoteBucket(r), 60, time.Minute); err != nil {
		s.problem(w, r, err)
		return
	}
	a, err := s.accounts.FeedActor(r.Context(), chi.URLParam(r, "secret"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	today := utcDay(time.Now())
	releases, err := s.mergedCalendar(r.Context(), a, today.AddDate(0, 0, -30), today.AddDate(0, 0, 180), r.URL.Query().Get("filter"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(calendarICS(releases, time.Now().UTC())))
}

func ics(releases []library.Release, now time.Time) string {
	return calendarICS(mergeCalendar(releases, nil, nil, "all"), now)
}
