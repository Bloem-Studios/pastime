package imports

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/jackc/pgx/v5"
)

// ListensJob records listens submitted through the ListenBrainz-compatible endpoint.
// Matching albums can take provider calls, so submissions are acknowledged once queued.
const ListensJob = "listens"

// Listen is one submitted listen, reduced to what identifies the album.
type Listen struct {
	At           time.Time `json:"at"`
	Artist       string    `json:"artist"`
	Track        string    `json:"track"`
	Release      string    `json:"release"`
	ReleaseMBID  string    `json:"release_mbid,omitempty"`
	GroupMBID    string    `json:"release_group_mbid,omitempty"`
	TrackNumber  *int      `json:"track_number,omitempty"`
	RecordingKey string    `json:"recording"`
}

type listensPayload struct {
	ProfileID int64    `json:"profile_id,string"`
	Listens   []Listen `json:"listens"`
}

// ParseListens reads a ListenBrainz submit-listens document. Now-playing notices carry
// no time and are not listens; they are reported as zero listens.
func ParseListens(body []byte, now time.Time) ([]Listen, error) {
	var doc struct {
		Type    string `json:"listen_type"`
		Payload []struct {
			ListenedAt json.Number `json:"listened_at"`
			Track      struct {
				Artist  string `json:"artist_name"`
				Track   string `json:"track_name"`
				Release string `json:"release_name"`
				Info    struct {
					TrackNumber       json.RawMessage `json:"tracknumber"`
					LegacyTrackNumber json.RawMessage `json:"track_number"`
					ReleaseMBID       string          `json:"release_mbid"`
					GroupMBID         string          `json:"release_group_mbid"`
					RecordingMBID     string          `json:"recording_mbid"`
				} `json:"additional_info"`
			} `json:"track_metadata"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fault.Invalid("The body is not a ListenBrainz submission.")
	}
	switch doc.Type {
	case "playing_now":
		return nil, nil
	case "single", "import":
	default:
		return nil, fault.Invalid("listen_type must be single, import or playing_now.")
	}
	if len(doc.Payload) == 0 || len(doc.Payload) > 1000 || (doc.Type == "single" && len(doc.Payload) != 1) {
		return nil, fault.Invalid("Submit one listen, or up to 1000 for an import.")
	}
	out := make([]Listen, 0, len(doc.Payload))
	for _, p := range doc.Payload {
		ts, err := p.ListenedAt.Int64()
		at := time.Unix(ts, 0).UTC()
		if err != nil || ts <= 0 || at.After(now.Add(time.Hour)) {
			return nil, fault.Invalid("Each listen needs listened_at as seconds since 1970.")
		}
		t := p.Track
		if strings.TrimSpace(t.Artist) == "" || strings.TrimSpace(t.Track) == "" || len(t.Artist) > 500 || len(t.Track) > 500 || len(t.Release) > 500 {
			return nil, fault.Invalid("Each listen needs artist_name and track_name.")
		}
		number, err := listenTrackNumber(t.Info.TrackNumber, t.Info.LegacyTrackNumber)
		if err != nil {
			return nil, err
		}
		key := t.Info.RecordingMBID
		if key == "" {
			key = strings.ToLower(t.Artist + "|" + t.Track)
		}
		out = append(out, Listen{At: at, Artist: strings.TrimSpace(t.Artist), Track: strings.TrimSpace(t.Track), Release: strings.TrimSpace(t.Release),
			TrackNumber: number, ReleaseMBID: t.Info.ReleaseMBID, GroupMBID: t.Info.GroupMBID, RecordingKey: key})
	}
	return out, nil
}

// QueueListens stores submitted listens for the worker.
func (s Store) QueueListens(ctx context.Context, userID int64, listens []Listen) error {
	if len(listens) == 0 {
		return nil
	}
	payload, _ := json.Marshal(listensPayload{ProfileID: userID, Listens: listens})
	key := fmt.Sprintf("listens:%d:%x", userID, sha256.Sum256(payload))
	_, err := jobs.Enqueue(ctx, s.Pool, ListensJob, key, "idempotent", payload, 5)
	return err
}

// ListensHandler records each listen as a play of its album. Listens whose album cannot
// be identified are dropped. Plays share the linked ListenBrainz service's identity, so a
// listen both submitted here and pulled from ListenBrainz is recorded once.
func (s Store) ListensHandler(ctx context.Context, raw json.RawMessage) error {
	var p listensPayload
	if json.Unmarshal(raw, &p) != nil || p.ProfileID == 0 {
		return jobs.ErrInvalidPayload
	}
	var active bool
	if err := s.Pool.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL", p.ProfileID).Scan(&active); errors.Is(err, pgx.ErrNoRows) || !active {
		return nil
	} else if err != nil {
		return err
	}
	var m merger
	for _, l := range p.Listens {
		if l.Release == "" && l.GroupMBID == "" && l.ReleaseMBID == "" {
			continue
		}
		r := albumRecord(&m, l.GroupMBID, l.ReleaseMBID, l.Artist, l.Release)
		r.Plays = append(r.Plays, PlayRef{At: l.At, ChildID: recordingID(l.RecordingKey), ChildTitle: l.Track, TrackNumber: l.TrackNumber, ExternalID: "listenbrainz:" + strconv.FormatInt(l.At.Unix(), 10) + ":" + l.Track})
	}
	shows := map[int64]map[[2]int]int64{}
	var c counters
	for _, rec := range m.records() {
		item, err := s.resolve(ctx, rec)
		var fe *fault.Error
		if errors.As(err, &fe) && fe.Status == 404 {
			continue
		}
		if err != nil {
			return err
		}
		if err = s.applyTo(ctx, p.ProfileID, "", "listenbrainz", item, rec, shows, &c); err != nil {
			return err
		}
	}
	return nil
}

// Navidrome versions use both names and JSON string/number values. Ignore absent
// metadata; reject malformed positions instead of assigning the wrong recording.
func listenTrackNumber(canonical, legacy json.RawMessage) (*int, error) {
	parse := func(raw json.RawMessage) (*int, error) {
		if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
			return nil, nil
		}
		value := string(raw)
		if raw[0] == '"' {
			if json.Unmarshal(raw, &value) != nil {
				return nil, ErrFormat
			}
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100000 {
			return nil, fault.Invalid("Invalid track number.")
		}
		return &n, nil
	}
	a, err := parse(canonical)
	if err != nil {
		return nil, err
	}
	b, err := parse(legacy)
	if err != nil {
		return nil, err
	}
	if a != nil && b != nil && *a != *b {
		return nil, fault.Invalid("Conflicting track numbers.")
	}
	if a != nil {
		return a, nil
	}
	return b, nil
}
