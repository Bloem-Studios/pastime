package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/catalog"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

func (s *Server) bloemRoutes() {
	s.bloemListSnapshotRoutes()
	s.bloemLibraryRoutes()
	s.bloemAvailabilityRoutes()
	s.bloemCalendarRoutes()
	s.aiRecommendationsRoutes()
	s.bloemPersonalRoutes()
	s.bloemHandoffRoutes()
	s.siloConnectRoutes()
	s.Router.Post("/api/v1/integrations/bloem/credentials:inspect", s.bloemInspect)
	s.Router.Post("/api/v1/integrations/bloem/events:apply", s.bloemApply)
	s.Router.Post("/api/v1/integrations/bloem/snapshots:page", s.bloemSnapshotPage)
	s.Router.Post("/api/v1/integrations/bloem/snapshots:complete", s.bloemSnapshotComplete)
	s.Router.Post("/api/v1/integrations/bloem/profiles:provision", s.bloemProvision)
	s.Router.Post("/api/v1/integrations/bloem/profiles/{profileID}/credentials:issue", s.bloemIssueCredential)
}
func (s *Server) bloemStore() bloem.Store { return bloem.Store{Pool: s.library.Pool, Box: s.secrets} }
func (s *Server) bloemProvisionToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return "", false
	}
	if err := s.accounts.Limit(r.Context(), "bloem-native:"+remoteBucket(r), 1200, time.Minute); err != nil {
		s.problem(w, r, err)
		return "", false
	}
	// Machine provisioning accepts only its backend bearer credential. Cookies,
	// personal tokens and a UI profile header confer no provisioning authority.
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 {
		s.problem(w, r, fault.Unauthenticated)
		return "", false
	}
	raw, ok := strings.CutPrefix(headers[0], "Bearer ")
	if !ok || !strings.HasPrefix(raw, "ptbp_") || strings.TrimSpace(raw) != raw {
		s.problem(w, r, fault.Unauthenticated)
		return "", false
	}
	if _, ok := r.Header[http.CanonicalHeaderKey("X-Pastime-Profile-ID")]; ok {
		s.problem(w, r, fault.Forbidden)
		return "", false
	}
	if _, err := s.bloemStore().AuthenticateProvision(r.Context(), raw); err != nil {
		s.problem(w, r, err)
		return "", false
	}
	return raw, true
}
func (s *Server) bloemProvision(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	in, err := decode[struct {
		bloem.ProvisionInput
		AccountActive *bool `json:"account_active"`
		Active        *bool `json:"active"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.AccountActive == nil || in.Active == nil {
		s.problem(w, r, fault.Invalid("Send explicit account and profile activity states."))
		return
	}
	in.ProvisionInput.AccountActive = *in.AccountActive
	in.ProvisionInput.Active = *in.Active
	out, err := s.bloemStore().Provision(r.Context(), raw, in.ProvisionInput)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) bloemIssueCredential(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	profileID, err := identity.ParseID(chi.URLParam(r, "profileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[struct {
		IssuanceID string `json:"issuance_id"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.bloemStore().IssueCredential(r.Context(), raw, profileID, in.IssuanceID)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}

func (s *Server) bloemSyncToken(w http.ResponseWriter, r *http.Request) (string, bloem.SyncAuthority, bool) {
	var zero bloem.SyncAuthority
	if s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return "", zero, false
	}
	// An address that keeps presenting keys Pastime never issued is refused before any
	// lookup. Only such keys count: a Silo server holding a revoked or expired key of
	// one connection must not stop the others that share its address.
	address := remoteBucket(r)
	if err := s.accounts.LimitReached(r.Context(), "bloem-sync-fail:"+address, syncFailuresPerMinute); err != nil {
		s.problem(w, r, err)
		return "", zero, false
	}
	// The address ceiling bounds all sync traffic from one address, authenticated or not.
	if err := s.accounts.Limit(r.Context(), "bloem-playback:"+address, syncAddressPerMinute, time.Minute); err != nil {
		s.problem(w, r, err)
		return "", zero, false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		s.problem(w, r, fault.Unauthenticated)
		return "", zero, false
	}
	raw, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || !strings.HasPrefix(raw, "ptbs_") || strings.TrimSpace(raw) != raw {
		s.problem(w, r, fault.Unauthenticated)
		return "", zero, false
	}
	if _, ok := r.Header[http.CanonicalHeaderKey("X-Pastime-Profile-ID")]; ok {
		s.problem(w, r, fault.Forbidden)
		return "", zero, false
	}
	a, err := s.bloemStore().AuthenticateSync(r.Context(), raw)
	if err != nil {
		if errors.Is(err, bloem.ErrUnknownSyncKey) {
			// The answer stays 401 even when counting fails.
			_ = s.accounts.Limit(r.Context(), "bloem-sync-fail:"+address, syncFailuresPerMinute, time.Minute)
		}
		if errors.Is(err, bloem.ErrServiceAccess) && !plainPluginCaller(r) {
			// Plugin 0.1.14 turns a 403 into a permission fault that spends Silo's
			// export attempts; before release A it got 401 here.
			err = fault.Unauthenticated
		}
		s.problem(w, r, err)
		return "", zero, false
	}
	// Each credential has its own budget, so one busy profile cannot starve the others
	// that share the server's address. Buckets are keyed by credential ID: their number
	// is bounded by the keys Pastime issued.
	if err = s.accounts.Limit(r.Context(), "bloem-sync:"+a.CredentialID, s.syncPerMinute, time.Minute); err != nil {
		s.problem(w, r, err)
		return "", zero, false
	}
	return raw, a, true
}

// Answers added in release A that plugin 0.1.14 would mishandle go only to plugin
// 0.2.0 or later, which identifies itself as User-Agent bloem-pastime/<version> and
// sends wire event IDs pevt_<digest>. 0.1.14 sends Go's default User-Agent and
// bloem_evt_ event IDs, and keeps the answers it had before release A.
func plainPluginCaller(r *http.Request) bool {
	return strings.HasPrefix(r.UserAgent(), "bloem-pastime/")
}

// retriesUnlistedEpisodes reports whether the sender of a playback event retries a
// 503 catalog_pending for that event alone. Plugin 0.1.14 makes every later event of
// its batch retry too, spending their attempts, so it keeps the 404 no_match it got
// before release A.
func retriesUnlistedEpisodes(eventID string) bool {
	return strings.HasPrefix(eventID, "pevt_")
}
func (s *Server) bloemInspect(w http.ResponseWriter, r *http.Request) {
	_, a, ok := s.bloemSyncToken(w, r)
	if !ok {
		return
	}
	if _, err := decode[struct{}](w, r); err != nil {
		s.problem(w, r, err)
		return
	}
	var name string
	if err := s.library.Pool.QueryRow(r.Context(), `SELECT name FROM tracking_profiles WHERE id=$1`, a.ProfileID).Scan(&name); err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, struct {
		InstanceID string `json:"instance_id"`
		AccountID  string `json:"account_id"`
		ProfileID  string `json:"profile_id"`
		Name       string `json:"name"`
	}{a.InstanceID, a.SourceAccountID, a.SourceProfileID, name})
}

type bloemMedia struct {
	Kind              string            `json:"kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	Season            *int              `json:"season,omitempty"`
	Episode           *int              `json:"episode,omitempty"`
	// Title and AirDate describe an episode: they check its identifiers and numbers,
	// and find it by air date when another catalog numbers the show differently.
	// SeriesTitle finds the show only when none of its identifiers does. A movie's are
	// ignored.
	Title       string `json:"title,omitempty"`
	AirDate     string `json:"air_date,omitempty"`
	SeriesTitle string `json:"series_title,omitempty"`
}

func (s *Server) bloemApply(w http.ResponseWriter, r *http.Request) {
	raw, a, ok := s.bloemSyncToken(w, r)
	if !ok {
		return
	}
	in, err := decode[struct {
		bloem.PlaybackEvent
		Completed *bool      `json:"completed"`
		Media     bloemMedia `json:"media"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.Completed == nil {
		s.problem(w, r, fault.Invalid("Send explicit host completion."))
		return
	}
	in.PlaybackEvent.Completed = *in.Completed
	if in.InstanceID != a.InstanceID || in.AccountID != a.SourceAccountID || in.ProfileID != a.SourceProfileID {
		s.problem(w, r, fault.Forbidden)
		return
	}
	if err = bloem.ValidateEvent(in.PlaybackEvent); err != nil {
		s.problem(w, r, err)
		return
	}
	title, target, err := s.bloemPlaybackTarget(r.Context(), in.Media)
	if err != nil {
		if catalog.NotListedYet(err) && !retriesUnlistedEpisodes(in.EventID) {
			err = catalog.ErrNoMatch
		}
		s.problem(w, r, err)
		return
	}
	out, err := s.bloemStore().ApplyMatchedEvent(r.Context(), raw, in.PlaybackEvent, title, target)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

var imdbID = regexp.MustCompile(`^tt[0-9]{1,18}$`)

// bloemPlaybackTarget validates playback media and resolves its catalog title and
// target (the episode, or the movie itself) by stable identifiers.
func (s *Server) bloemPlaybackTarget(ctx context.Context, media bloemMedia) (int64, int64, error) {
	ref := catalog.Reference{Kind: media.Kind, TMDB: media.ExternalIDs["tmdb"], IMDB: media.ExternalIDs["imdb"], TVDB: media.ExternalIDs["tvdb"], ShowTMDB: media.SeriesExternalIDs["tmdb"], ShowIMDB: media.SeriesExternalIDs["imdb"], ShowTVDB: media.SeriesExternalIDs["tvdb"], Season: media.Season, Episode: media.Episode}
	if media.Kind != "movie" && media.Kind != "episode" || media.Kind == "movie" && len(media.ExternalIDs) == 0 || media.Kind == "episode" && (len(media.SeriesExternalIDs) == 0 || media.Season == nil || *media.Season < 0 || media.Episode == nil || *media.Episode < 1) {
		return 0, 0, fault.Invalid("Send stable movie or episode identifiers.")
	}
	for _, ids := range []map[string]string{media.ExternalIDs, media.SeriesExternalIDs} {
		for key, value := range ids {
			if key != "tmdb" && key != "tvdb" && key != "imdb" || value == "" || len(value) > 80 {
				return 0, 0, fault.Invalid("Unsupported media identifier.")
			}
			if key == "imdb" {
				if !imdbID.MatchString(value) {
					return 0, 0, fault.Invalid("Invalid media identifier.")
				}
			} else {
				if n, err := strconv.ParseInt(value, 10, 64); err != nil || n < 1 || strconv.FormatInt(n, 10) != value {
					return 0, 0, fault.Invalid("Invalid media identifier.")
				}
			}
		}
	}
	if utf8.RuneCountInString(media.Title) > 500 || utf8.RuneCountInString(media.SeriesTitle) > 500 {
		return 0, 0, fault.Invalid("Episode title is too long.")
	}
	if _, err := time.Parse(time.DateOnly, media.AirDate); media.AirDate != "" && err != nil {
		return 0, 0, fault.Invalid("Send the episode air date as YYYY-MM-DD.")
	}
	if media.Kind == "episode" {
		ref.Title, ref.AirDate, ref.ShowTitle = media.Title, media.AirDate, strings.TrimSpace(media.SeriesTitle)
	}
	if s.catalog == nil {
		return 0, 0, fault.Unavailable
	}
	matched, err := s.catalog.MatchVideo(ctx, ref)
	if err != nil {
		return 0, 0, err
	}
	target := matched.Title.ID
	if matched.Episode != nil {
		target = matched.Episode.ID
	}
	return matched.Title.ID, target, nil
}
func (s *Server) bloemSnapshotPage(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	in, err := decode[struct {
		bloem.SnapshotPage
		Complete *bool `json:"complete"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.Complete == nil {
		s.problem(w, r, fault.Invalid("Send explicit snapshot completeness."))
		return
	}
	in.SnapshotPage.Complete = *in.Complete
	out, err := s.bloemStore().AcceptPage(r.Context(), raw, in.SnapshotPage)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) bloemSnapshotComplete(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	in, err := decode[struct {
		SnapshotID string `json:"snapshot_id"`
		Receipt    string `json:"receipt"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if err = s.bloemStore().CompleteSnapshot(r.Context(), raw, in.SnapshotID, in.Receipt); err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, struct {
		Completed bool `json:"completed"`
	}{true})
}
