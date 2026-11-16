package provider

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AniList covers anime and manga through its public GraphQL API.
type AniList struct{ Client *Client }

func (AniList) Name() string    { return "anilist" }
func (AniList) Kinds() []string { return []string{"anime", "manga"} }

const aniFields = `id idMal title{romaji english native} coverImage{extraLarge large} bannerImage startDate{year month day}
episodes chapters volumes duration status format genres averageScore description(asHtml:false) studios(isMain:true){nodes{name}}
staff(perPage:6,sort:RELEVANCE){nodes{name{full}}}`

type aniMedia struct {
	ID              int64  `json:"id"`
	Type            string `json:"type"`
	Recommendations struct {
		Nodes []struct {
			Media *aniMedia `json:"mediaRecommendation"`
		} `json:"nodes"`
	} `json:"recommendations"`
	IDMal int64 `json:"idMal"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	Cover struct {
		ExtraLarge string `json:"extraLarge"`
		Large      string `json:"large"`
	} `json:"coverImage"`
	Banner    string `json:"bannerImage"`
	StartDate struct {
		Year, Month, Day int
	} `json:"startDate"`
	Episodes    int      `json:"episodes"`
	Chapters    int      `json:"chapters"`
	Volumes     int      `json:"volumes"`
	Duration    int      `json:"duration"`
	Status      string   `json:"status"`
	Format      string   `json:"format"`
	Genres      []string `json:"genres"`
	Score       int      `json:"averageScore"`
	Description string   `json:"description"`
	Studios     struct {
		Nodes []struct{ Name string } `json:"nodes"`
	} `json:"studios"`
	Staff struct {
		Nodes []struct {
			Name struct{ Full string } `json:"name"`
		} `json:"nodes"`
	} `json:"staff"`
}

var htmlTag = regexp.MustCompile(`<[^>]+>`)

func (a AniList) query(ctx context.Context, query string, variables map[string]any, out any) error {
	return a.Client.JSON(ctx, Request{Method: "POST", URL: "https://graphql.anilist.co", Body: map[string]any{"query": query, "variables": variables}, Retries: 1}, out)
}

func aniType(kind string) (string, error) {
	switch kind {
	case "anime":
		return "ANIME", nil
	case "manga":
		return "MANGA", nil
	}
	return "", ErrUnsupported
}

func (a AniList) record(kind string, m aniMedia) Record {
	title := m.Title.English
	if title == "" {
		title = m.Title.Romaji
	}
	r := Record{
		Provider: "anilist", Kind: kind, ProviderID: strconv.FormatInt(m.ID, 10), Title: title, OriginalTitle: m.Title.Native,
		Overview: strings.TrimSpace(htmlTag.ReplaceAllString(m.Description, "")), ImageURL: m.Cover.ExtraLarge, BackdropURL: m.Banner,
		ReleaseStatus: strings.ToLower(m.Status), Genres: m.Genres, ExternalIDs: map[string]string{}, Details: map[string]any{},
	}
	if r.ImageURL == "" {
		r.ImageURL = m.Cover.Large
	}
	if m.Title.Romaji != "" && m.Title.Romaji != title {
		r.Details["romaji_title"] = clean(m.Title.Romaji, 500)
	}
	if m.StartDate.Year > 0 {
		month, day := max(m.StartDate.Month, 1), max(m.StartDate.Day, 1)
		r.ReleaseDate = date(strconv.Itoa(m.StartDate.Year) + "-" + pad2(month) + "-" + pad2(day))
	}
	if m.Score > 0 {
		r.Rating = floatPtr(float64(m.Score) / 10)
	}
	if m.IDMal > 0 {
		r.ExternalIDs["mal"] = strconv.FormatInt(m.IDMal, 10)
	}
	if m.Format != "" {
		r.Details["format"] = strings.ToLower(m.Format)
	}
	for _, s := range m.Studios.Nodes {
		r.Credits = append(r.Credits, s.Name)
	}
	for _, s := range m.Staff.Nodes {
		r.Credits = append(r.Credits, s.Name.Full)
	}
	if kind == "anime" {
		r.Total, r.RuntimeMinutes = intPtr(m.Episodes), intPtr(m.Duration)
	} else {
		r.Total = intPtr(m.Chapters)
		if m.Volumes > 0 {
			r.Details["volumes"] = m.Volumes
		}
	}
	return r
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func (a AniList) Search(ctx context.Context, kind, query string) ([]Record, error) {
	page, err := a.Query(ctx, kind, QueryOptions{Query: query})
	return page.Items, err
}

func (a AniList) Fetch(ctx context.Context, kind, id string) (Record, error) {
	t, err := aniType(kind)
	if err != nil {
		return Record{}, err
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	var out struct {
		Errors []any `json:"errors"`
		Data   struct {
			Media *aniMedia `json:"Media"`
		} `json:"data"`
	}
	if err = a.query(ctx, `query($id:Int,$t:MediaType){Media(id:$id,type:$t){`+aniFields+` recommendations(perPage:20,sort:RATING_DESC){nodes{mediaRecommendation{id type title{english romaji native} coverImage{extraLarge large} startDate{year month day}}}}}}`, map[string]any{"id": n, "t": t}, &out); err != nil {
		return Record{}, err
	}
	if len(out.Errors) > 0 {
		return Record{}, ErrUnavailable
	}
	if out.Data.Media == nil {
		return Record{}, ErrNotFound
	}
	r := a.record(kind, *out.Data.Media)
	related := []map[string]any{}
	for _, node := range out.Data.Media.Recommendations.Nodes {
		if node.Media == nil || node.Media.ID == n || len(related) == 20 {
			continue
		}
		relatedKind := map[string]string{"ANIME": "anime", "MANGA": "manga"}[node.Media.Type]
		if relatedKind != kind {
			continue
		}
		item := a.record(kind, *node.Media)
		if item.Title == "" {
			continue
		}
		related = append(related, map[string]any{"provider": "anilist", "kind": kind, "id": item.ProviderID, "title": item.Title, "image_url": item.ImageURL, "year": item.Year()})
	}
	r.Details["related"] = related
	return r, nil
}

// ListEntry is one row of a user's public AniList list.
type ListEntry struct {
	MediaID     int64
	Kind        string // anime or manga
	Status      string // AniList status: CURRENT, PLANNING, COMPLETED, DROPPED, PAUSED, REPEATING
	Progress    int
	Score       float64 // POINT_10 scale, 0 when unscored
	Repeat      int
	Notes       string
	CompletedAt *time.Time
}

// UserLists reads a user's public anime and manga lists. Private lists are not visible
// to the public API and are reported as not found.
func (a AniList) UserLists(ctx context.Context, user string) ([]ListEntry, error) {
	var out []ListEntry
	for _, kind := range []string{"anime", "manga"} {
		t, _ := aniType(kind)
		var resp struct {
			Data struct {
				Collection *struct {
					Lists []struct {
						Entries []struct {
							Status      string                         `json:"status"`
							Progress    int                            `json:"progress"`
							Score       float64                        `json:"score"`
							Repeat      int                            `json:"repeat"`
							Notes       string                         `json:"notes"`
							CompletedAt struct{ Year, Month, Day int } `json:"completedAt"`
							Media       struct {
								ID int64 `json:"id"`
							} `json:"media"`
						} `json:"entries"`
					} `json:"lists"`
				} `json:"MediaListCollection"`
			} `json:"data"`
		}
		q := `query($u:String,$t:MediaType){MediaListCollection(userName:$u,type:$t){lists{entries{status progress score(format:POINT_10_DECIMAL) repeat notes completedAt{year month day} media{id}}}}}`
		if err := a.query(ctx, q, map[string]any{"u": user, "t": t}, &resp); err != nil {
			return nil, err
		}
		if resp.Data.Collection == nil {
			return nil, ErrNotFound
		}
		for _, l := range resp.Data.Collection.Lists {
			for _, e := range l.Entries {
				entry := ListEntry{MediaID: e.Media.ID, Kind: kind, Status: e.Status, Progress: e.Progress, Score: e.Score, Repeat: e.Repeat, Notes: e.Notes}
				if c := e.CompletedAt; c.Year > 0 {
					at := time.Date(c.Year, time.Month(max(c.Month, 1)), max(c.Day, 1), 12, 0, 0, 0, time.UTC)
					entry.CompletedAt = &at
				}
				out = append(out, entry)
			}
		}
	}
	return out, nil
}

// FindExternal maps a MyAnimeList id to the AniList id.
func (a AniList) FindExternal(ctx context.Context, kind, scheme, value string) (string, error) {
	t, err := aniType(kind)
	if err != nil || scheme != "mal" {
		return "", ErrNotFound
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 {
		return "", ErrNotFound
	}
	var out struct {
		Data struct {
			Media *struct {
				ID int64 `json:"id"`
			} `json:"Media"`
		} `json:"data"`
	}
	if err = a.query(ctx, `query($id:Int,$t:MediaType){Media(idMal:$id,type:$t){id}}`, map[string]any{"id": n, "t": t}, &out); err != nil {
		return "", err
	}
	if out.Data.Media == nil {
		return "", ErrNotFound
	}
	return strconv.FormatInt(out.Data.Media.ID, 10), nil
}
