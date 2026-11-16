package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

func (s *Server) bloemPersonalRoutes() {
	s.Router.Post("/api/v1/integrations/bloem/state/events:apply", s.bloemPersonalApply)
	s.Router.Post("/api/v1/integrations/bloem/state:read", s.bloemPersonalRead)
}
func (s *Server) bloemPersonalApply(w http.ResponseWriter, r *http.Request) {
	raw, a, ok := s.bloemSyncToken(w, r)
	if !ok {
		return
	}
	in, err := decode[struct {
		bloem.PersonalEvent
		Media bloemPersonalMedia `json:"media"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.InstanceID != a.InstanceID || in.AccountID != a.SourceAccountID || in.ProfileID != a.SourceProfileID {
		s.problem(w, r, fault.Forbidden)
		return
	}
	if _, err = bloem.ValidatePersonalEvent(in.PersonalEvent); err != nil {
		s.problem(w, r, err)
		return
	}
	title, err := s.bloemPersonalTitle(r.Context(), in.Media)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.bloemStore().ApplyMatchedPersonalEvent(r.Context(), raw, in.PersonalEvent, title)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}

// bloemPersonalMedia is a personal-state title. The playback media schema stays
// separate: only this one accepts a catalog item.
type bloemPersonalMedia struct {
	Kind              string            `json:"kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	Season            *int              `json:"season,omitempty"`
	Episode           *int              `json:"episode,omitempty"`
	// CatalogItemID names a top-level catalog title by the provider_item_key
	// catalog:<id> that state:read reported. Used only without external IDs.
	CatalogItemID catalogItemID `json:"catalog_item_id,omitempty"`
}

// catalogItemID is a positive decimal catalog identifier sent as a JSON string, or as
// a JSON number by clients that parsed it from a catalog:<id> key.
type catalogItemID int64

func (c *catalogItemID) UnmarshalJSON(b []byte) error {
	text := string(b)
	if text == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		unquoted, err := strconv.Unquote(text)
		if err != nil {
			return err
		}
		text = unquoted
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != text {
		return errors.New("invalid catalog item identifier")
	}
	*c = catalogItemID(n)
	return nil
}

// Resolve known catalog identifiers without requiring external-provider access.
// Conflicting namespaces or ambiguous local identities are never guessed.
func (s *Server) bloemPersonalTitle(ctx context.Context, media bloemPersonalMedia) (int64, error) {
	if media.Kind != "movie" && media.Kind != "series" || len(media.ExternalIDs) > 3 || len(media.SeriesExternalIDs) > 0 || media.Season != nil || media.Episode != nil || len(media.ExternalIDs) == 0 && media.CatalogItemID == 0 {
		return 0, fault.Invalid("Send stable movie or series identifiers.")
	}
	if len(media.ExternalIDs) == 0 {
		// A title Silo knows only by the catalog key Pastime reported, such as a
		// removal of an item it imported from state:read.
		var id int64
		err := s.library.Pool.QueryRow(ctx, `SELECT id FROM catalog_items WHERE id=$1 AND kind=$2 AND parent_id IS NULL`, int64(media.CatalogItemID), media.Kind).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fault.NotFound
		}
		return id, err
	}
	var selected int64
	for ns, value := range media.ExternalIDs {
		if ns != "tmdb" && ns != "imdb" && ns != "tvdb" || len(value) > 80 {
			return 0, fault.Invalid("Unsupported media identifier.")
		}
		if ns == "imdb" {
			if !imdbID.MatchString(value) {
				return 0, fault.Invalid("Invalid media identifier.")
			}
		} else {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 1 || strconv.FormatInt(n, 10) != value {
				return 0, fault.Invalid("Invalid media identifier.")
			}
		}
	}
	for ns, value := range media.ExternalIDs {
		rows, err := s.library.Pool.Query(ctx, `SELECT id,provider,provider_id,external_ids FROM catalog_items WHERE kind=$1 AND parent_id IS NULL AND ((provider=$2 AND provider_id=$3) OR external_ids->>$2=$3) ORDER BY id LIMIT 2`, media.Kind, ns, value)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id int64
			var provider, providerID string
			var raw []byte
			if err = rows.Scan(&id, &provider, &providerID, &raw); err != nil {
				rows.Close()
				return 0, err
			}
			if selected != 0 && selected != id {
				rows.Close()
				return 0, fault.Conflict
			}
			selected = id
			var ids map[string]any
			if err = json.Unmarshal(raw, &ids); err != nil {
				rows.Close()
				return 0, err
			}
			for other, want := range media.ExternalIDs {
				known, _ := ids[other].(string)
				if other == provider {
					known = providerID
				}
				if known != "" && known != want {
					rows.Close()
					return 0, fault.Conflict
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
	}
	if selected != 0 {
		return selected, nil
	}
	if s.catalog == nil {
		return 0, fault.Unavailable
	}
	ref := catalog.Reference{Kind: media.Kind, TMDB: media.ExternalIDs["tmdb"], IMDB: media.ExternalIDs["imdb"], TVDB: media.ExternalIDs["tvdb"]}
	if media.Kind == "series" {
		// Series matching reads only the show identifiers.
		ref = catalog.Reference{Kind: media.Kind, ShowTMDB: media.ExternalIDs["tmdb"], ShowIMDB: media.ExternalIDs["imdb"], ShowTVDB: media.ExternalIDs["tvdb"]}
	}
	match, err := s.catalog.MatchVideo(ctx, ref)
	return match.Title.ID, err
}

func (s *Server) bloemPersonalRead(w http.ResponseWriter, r *http.Request) {
	raw, _, ok := s.bloemSyncToken(w, r)
	if !ok {
		return
	}
	in, err := decode[bloem.PersonalRead](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.bloemStore().ReadPersonalState(r.Context(), raw, in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}
