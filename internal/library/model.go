package library

import (
	"strings"
	"time"
	"unicode"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

type Summary struct {
	ScoredAt        *time.Time `json:"scored_at"`
	ID              int64      `json:"id,string"`
	Title           string     `json:"title"`
	MediaType       string     `json:"media_type"`
	Year            *int       `json:"year"`
	Unit            string     `json:"unit"`
	Total           *int       `json:"total"`
	Status          string     `json:"status"`
	Progress        int        `json:"progress"`
	Rating          *int       `json:"rating"`
	Cycle           int        `json:"cycle"`
	CompletedCycles int        `json:"completed_cycles"`
	Version         int        `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CatalogID       *int64     `json:"catalog_id,string"`
	ImageURL        string     `json:"image_url"`
	BackdropURL     string     `json:"backdrop_url"`
	OriginalTitle   string     `json:"original_title"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	LastActivityAt  *time.Time `json:"last_activity_at"`
	// LocalizedTitle is the title in the person's chosen language, when known.
	LocalizedTitle *string `json:"localized_title"`
	// LastPlayedAt is the latest play that was a viewing; bulk marks do not count.
	LastPlayedAt *time.Time `json:"last_played_at"`
	// ListNote is set only when browsing a manual list.
	ListNote *string `json:"list_note,omitempty"`
}
type Entry struct {
	Summary
	Notes   string   `json:"notes"`
	Edition *Edition `json:"edition"`
	// EpisodeOrder is the alternate episode order chosen for a show, when any.
	EpisodeOrder *string `json:"episode_order"`
	// NextItem is a show's next episode to watch, when it has one.
	NextItem *NextItem `json:"next_item"`
}

// NextItem is the first regular episode, in the show's watch order, with no play or skip
// in the current cycle. It may not have aired yet.
type NextItem struct {
	CatalogID      int64   `json:"catalog_id,string"`
	SeasonNumber   *int    `json:"season_number"`
	EpisodeNumber  *int    `json:"episode_number"`
	Title          string  `json:"title"`
	ReleaseDate    *string `json:"release_date"`
	RuntimeMinutes *int    `json:"runtime_minutes"`
}

// Edition is the specific printing or recording a person reads; its pages and cover
// replace the work's for them.
type Edition struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Publisher string `json:"publisher"`
	Year      string `json:"year"`
	Pages     *int   `json:"pages"`
	ISBN      string `json:"isbn"`
	Format    string `json:"format"`
	Language  string `json:"language"`
	ImageURL  string `json:"image_url"`
}
type EntryInput struct {
	Title     string `json:"title"`
	MediaType string `json:"media_type"`
	Year      *int   `json:"year"`
	Unit      string `json:"unit"`
	Total     *int   `json:"total"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Rating    *int   `json:"rating"`
	Notes     string `json:"notes"`
	Version   int    `json:"version"`
}
type ProgressInput struct {
	Version  int    `json:"version"`
	Progress *int   `json:"progress"`
	Status   string `json:"status"`
}
type Mutation struct {
	RemapMoved    int   `json:"-"`
	RemapRetained int   `json:"-"`
	RemapDropped  int   `json:"-"`
	ID            int64 `json:"id,string"`
	Version       int   `json:"version"`
	Replayed      bool  `json:"replayed"`
	Deleted       bool  `json:"deleted"`
}

// Filter selects and orders library entries. It is also the rule set of a smart list.
type Filter struct {
	Cursor, Query, MediaType, Status string
	MediaTypes, Statuses, Genres     []string
	TagIDs                           []int64
	MinRating, YearFrom, YearTo      *int
	Owned                            *bool
	// Provider and ProviderID find the entry for one provider record: the catalog
	// record's own provider and id, or an external id such as imdb or tvdb.
	Provider, ProviderID string
	ListID               int64
	Sort                 string // added, activity, title, rating, year, position
	Limit                int
	// anchored keeps the sort value out of the cursor: it names only the last entry,
	// whose value the server reads again. For readers who may not see that value.
	anchored bool
}
type Activity struct {
	ID        int64     `json:"id,string"`
	EntryID   int64     `json:"entry_id,string"`
	Title     string    `json:"title"`
	MediaType string    `json:"media_type"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	Unit      string    `json:"unit"`
	Cycle     int       `json:"cycle"`
	CreatedAt time.Time `json:"created_at"`
	// ImageURL and LocalizedTitle are the entry's current cover and title in the
	// preferred language, as on library summaries.
	ImageURL       string  `json:"image_url"`
	LocalizedTitle *string `json:"localized_title"`
	// ImportID and ImportSource name the import run that wrote the event, when one did.
	ImportID     *string `json:"import_id"`
	ImportSource *string `json:"import_source"`
}
type Stats struct {
	Total           int `json:"total"`
	InProgress      int `json:"in_progress"`
	Planned         int `json:"planned"`
	Completed       int `json:"completed"`
	CompletedCycles int `json:"completed_cycles"`
}
type List struct {
	ShareToken     *string    `json:"share_token"`
	ShareExpiresAt *time.Time `json:"share_expires_at"`
	ID             int64      `json:"id,string"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Visibility     string     `json:"visibility"`
	Slug           *string    `json:"slug"`
	Kind           string     `json:"kind"`
	Rules          Rules      `json:"rules"`
	Sort           string     `json:"sort"`
	Version        int        `json:"version"`
	ItemCount      int        `json:"item_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// Source is the outside list this one mirrors, when it follows one.
	Source *ListSource `json:"source"`
}

// ListSource is a Trakt or MDBList list that a list follows.
type ListSource struct {
	Kind     string     `json:"kind"`
	URL      string     `json:"url"`
	SyncedAt *time.Time `json:"synced_at"`
}

type ListInput struct {
	SnapshotOf  *string `json:"snapshot_of"`
	RotateShare bool    `json:"rotate_share"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Visibility  string  `json:"visibility"`
	Slug        *string `json:"slug"`
	Kind        string  `json:"kind"`
	Rules       Rules   `json:"rules"`
	Sort        string  `json:"sort"`
	Version     int     `json:"version"`
}

func oneOf(s string, options ...string) bool {
	for _, v := range options {
		if s == v {
			return true
		}
	}
	return false
}

// mediaTypes are the kinds of title a library entry can be.
var mediaTypes = []string{"movie", "series", "anime", "book", "manga", "comic", "game", "boardgame", "podcast", "music", "audiobook", "video"}

func validType(s string) bool {
	return oneOf(s, mediaTypes...)
}
func validStatus(s string) bool {
	return oneOf(s, "planned", "in_progress", "completed", "paused", "dropped", "rated")
}
func (in *EntryInput) validate(create bool) error {
	in.Title = strings.TrimSpace(in.Title)
	if len([]rune(in.Title)) < 1 || len([]rune(in.Title)) > 200 {
		return fault.Invalid("Title must contain 1 to 200 characters.")
	}
	for _, r := range in.Title {
		if unicode.IsControl(r) {
			return fault.Invalid("Title cannot contain control characters.")
		}
	}
	if !validType(in.MediaType) {
		return fault.Invalid("Choose a supported media type.")
	}
	if !oneOf(in.Unit, "minutes", "episodes", "pages", "chapters", "tracks", "items") {
		return fault.Invalid("Choose a supported progress unit.")
	}
	if create && in.Status == "" {
		in.Status = "planned"
	}
	if !validStatus(in.Status) {
		return fault.Invalid("Choose a supported status.")
	}
	if in.Year != nil && (*in.Year < 1 || *in.Year > 9999) {
		return fault.Invalid("Year must be between 1 and 9999.")
	}
	if in.Total != nil && (*in.Total < 1 || *in.Total > 100000000) {
		return fault.Invalid("Total must be between 1 and 100,000,000.")
	}
	if in.Progress < 0 || in.Progress > 100000000 || (in.Total != nil && in.Progress > *in.Total) {
		return fault.Invalid("Progress must be nonnegative and cannot exceed the total.")
	}
	if in.Rating != nil && (*in.Rating < 0 || *in.Rating > 10) {
		return fault.Invalid("Rating must be between 0 and 10.")
	}
	if len([]rune(in.Notes)) > 10000 {
		return fault.Invalid("Notes must contain at most 10,000 characters.")
	}
	if in.Status == "completed" && in.Total != nil {
		in.Progress = *in.Total
	}
	if !create && in.Version < 1 {
		return fault.Invalid("Include the current record version.")
	}
	return nil
}
