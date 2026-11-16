package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// Komga, Kavita and Kapowarr are self-hosted reading and comic servers. Komga and
// Kavita are read for reading progress only; Kapowarr is read for owned issues. None
// of them is ever written to.

const readerPageLimit = 200 // pages of results read per sync, bounding a huge library

// fetchKomga reads books the account has started or finished. A book with an ISBN, or
// an EPUB or PDF, is a book in its own right; image-based books (comics and manga) are
// grouped by series, with progress counted in finished issues.
func fetchKomga(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"X-API-Key": {cfg.Token}, "Content-Type": {"application/json"}}
	body := `{"condition":{"anyOf":[{"readStatus":{"operator":"is","value":"IN_PROGRESS"}},{"readStatus":{"operator":"is","value":"READ"}}]}}`
	type book struct {
		ID          string `json:"id"`
		SeriesID    string `json:"seriesId"`
		SeriesTitle string `json:"seriesTitle"`
		Name        string `json:"name"`
		Metadata    struct {
			Title   string `json:"title"`
			ISBN    string `json:"isbn"`
			Authors []struct {
				Name string `json:"name"`
				Role string `json:"role"`
			} `json:"authors"`
		} `json:"metadata"`
		Media struct {
			PagesCount   int    `json:"pagesCount"`
			MediaProfile string `json:"mediaProfile"`
		} `json:"media"`
		ReadProgress *struct {
			Page      int       `json:"page"`
			Completed bool      `json:"completed"`
			ReadDate  time.Time `json:"readDate"`
		} `json:"readProgress"`
	}
	out := []Record{}
	series := map[string]*Record{}
	order := []string{}
	for page := 0; page < readerPageLimit; page++ {
		var resp struct {
			Content []book `json:"content"`
			Last    bool   `json:"last"`
		}
		if _, err = getJSON(ctx, c, "POST", base+"/api/v1/books/list?size=500&page="+strconv.Itoa(page), header, strings.NewReader(body), &resp); err != nil {
			return nil, err
		}
		for _, b := range resp.Content {
			if b.ReadProgress == nil {
				continue
			}
			title := strings.TrimSpace(b.Metadata.Title)
			if title == "" {
				title = b.Name
			}
			profile := strings.ToUpper(b.Media.MediaProfile)
			if b.Metadata.ISBN != "" || profile == "EPUB" || profile == "PDF" {
				author := ""
				for _, a := range b.Metadata.Authors {
					if strings.EqualFold(a.Role, "writer") || author == "" {
						author = a.Name
					}
				}
				r := Record{Label: title, Kind: "book", Book: &catalog.BookReference{ISBN: b.Metadata.ISBN, Title: title, Author: author, Kind: "book"}, Status: "in_progress"}
				if b.ReadProgress.Completed {
					r.Status = "completed"
					r.Plays = []PlayRef{{At: b.ReadProgress.ReadDate.UTC(), ExternalID: "komga:" + b.ID}}
				} else if b.Media.PagesCount > 0 {
					r.Fraction = fraction(b.ReadProgress.Page, b.Media.PagesCount)
				}
				out = append(out, r)
				continue
			}
			r, ok := series[b.SeriesID]
			if !ok {
				r = &Record{Label: b.SeriesTitle, Kind: "comic", Title: &catalog.TitleReference{Title: b.SeriesTitle, Kind: "comic"}, Status: "in_progress", Progress: intp(0)}
				series[b.SeriesID] = r
				order = append(order, b.SeriesID)
			}
			if b.ReadProgress.Completed {
				*r.Progress++
			}
		}
		if resp.Last || len(resp.Content) == 0 {
			break
		}
	}
	for _, id := range order {
		out = append(out, *series[id])
	}
	return out, nil
}

// fetchKavita signs in with the account's Kavita API key and reads every series with
// reading progress. EPUB and PDF series are books; others are manga, or comics when
// Kavita knows their Comic Vine volume.
func fetchKavita(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	var user struct {
		Token string `json:"token"`
	}
	q := url.Values{"apiKey": {cfg.Token}, "pluginName": {"Pastime"}}
	if _, err = getJSON(ctx, c, "POST", base+"/api/Plugin/authenticate?"+q.Encode(), nil, nil, &user); err != nil {
		return nil, err
	}
	if user.Token == "" {
		return nil, ErrCredentials
	}
	header := http.Header{"Authorization": {"Bearer " + user.Token}, "Content-Type": {"application/json"}}
	// field 20 is ReadProgress, comparison 1 GreaterThan, combination 1 And.
	body := `{"statements":[{"comparison":1,"field":20,"value":"0"}],"combination":1,"limitTo":0}`
	type series struct {
		ID             int64     `json:"id"`
		Name           string    `json:"name"`
		Format         int       `json:"format"`
		Pages          int       `json:"pages"`
		PagesRead      int       `json:"pagesRead"`
		LatestReadDate time.Time `json:"latestReadDate"`
		AniListID      int64     `json:"aniListId"`
		MalID          int64     `json:"malId"`
		ComicVineID    *string   `json:"comicVineId"`
	}
	out := []Record{}
	for page := 1; page <= readerPageLimit; page++ {
		var batch []series
		if _, err = getJSON(ctx, c, "POST", base+"/api/Series/all-v2?PageSize=100&PageNumber="+strconv.Itoa(page), header, strings.NewReader(body), &batch); err != nil {
			return nil, err
		}
		for _, s := range batch {
			if s.PagesRead <= 0 || s.Name == "" {
				continue
			}
			r := Record{Label: s.Name, Status: "in_progress"}
			switch {
			case s.Format == 3 || s.Format == 4: // EPUB, PDF
				r.Kind, r.Book = "book", &catalog.BookReference{Title: s.Name, Kind: "book"}
			case s.AniListID > 0:
				r.Kind, r.Direct = "manga", &DirectRef{Provider: "anilist", Kind: "manga", ID: strconv.FormatInt(s.AniListID, 10)}
			case s.MalID > 0:
				r.Kind, r.External = "manga", &ExternalRef{Provider: "anilist", Kind: "manga", Scheme: "mal", Value: strconv.FormatInt(s.MalID, 10)}
			case s.ComicVineID != nil && comicVineVolume(*s.ComicVineID) != "":
				r.Kind, r.Direct = "comic", &DirectRef{Provider: "comicvine", Kind: "comic", ID: comicVineVolume(*s.ComicVineID)}
			default:
				r.Kind, r.Title = "manga", &catalog.TitleReference{Title: s.Name, Kind: "manga"}
			}
			if s.Pages > 0 && s.PagesRead >= s.Pages {
				r.Status = "completed"
				r.Plays = []PlayRef{{At: s.LatestReadDate.UTC(), ExternalID: "kavita:" + strconv.FormatInt(s.ID, 10)}}
			} else if s.Pages > 0 {
				r.Fraction = fraction(s.PagesRead, s.Pages)
			}
			out = append(out, r)
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
	return out, nil
}

// fetchKapowarr reads the volumes Kapowarr manages and records each issue that has a
// file as an owned digital copy, like Mylar3.
func fetchKapowarr(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	key := url.Values{"api_key": {cfg.Token}}.Encode()
	get := func(path string, out any) error {
		var envelope struct {
			Error  *string         `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if _, err := getJSON(ctx, c, "GET", base+"/api"+path+"?"+key, nil, nil, &envelope); err != nil {
			return err
		}
		if envelope.Error != nil && *envelope.Error != "" {
			if *envelope.Error == "ApiKeyInvalid" {
				return ErrCredentials
			}
			return fmt.Errorf("%w: Kapowarr did not complete the request", ErrFormat)
		}
		if json.Unmarshal(envelope.Result, out) != nil {
			return ErrFormat
		}
		return nil
	}
	var volumes []struct {
		ID               int64  `json:"id"`
		ComicVineID      int64  `json:"comicvine_id"`
		Title            string `json:"title"`
		IssuesDownloaded int    `json:"issues_downloaded"`
	}
	if err = get("/volumes", &volumes); err != nil {
		return nil, err
	}
	if len(volumes) > 2000 {
		return nil, fmt.Errorf("%w: Kapowarr library exceeds 2000 volumes", ErrFormat)
	}
	out := []Record{}
	total := 0
	for _, v := range volumes {
		if v.IssuesDownloaded <= 0 || v.ComicVineID < 1 {
			continue
		}
		var detail struct {
			Issues []struct {
				ComicVineID int64             `json:"comicvine_id"`
				IssueNumber string            `json:"issue_number"`
				Files       []json.RawMessage `json:"files"`
			} `json:"issues"`
		}
		if err = get("/volumes/"+strconv.FormatInt(v.ID, 10), &detail); err != nil {
			return nil, err
		}
		volume := strconv.FormatInt(v.ComicVineID, 10)
		r := Record{Label: v.Title, Kind: "comic", Status: "planned", Direct: &DirectRef{Provider: "comicvine", Kind: "comic", ID: volume}}
		for _, i := range detail.Issues {
			if len(i.Files) == 0 || i.ComicVineID < 1 {
				continue
			}
			total++
			if total > 100000 || len(r.Copies) >= 10000 {
				return nil, fmt.Errorf("%w: Kapowarr scan exceeds the copy limit", ErrFormat)
			}
			issue := strconv.FormatInt(i.ComicVineID, 10)
			r.Copies = append(r.Copies, library.ImportCopy{Format: "digital", Edition: "#" + i.IssueNumber, Store: "Kapowarr", ExternalID: volume + ":issue:" + issue, ChildID: issue})
		}
		if len(r.Copies) > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

// comicVineVolume accepts a Comic Vine volume id with or without its "4050-" prefix.
func comicVineVolume(raw string) string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "4050-")
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return strconv.FormatInt(n, 10)
	}
	return ""
}

func fraction(done, total int) *float64 {
	if total <= 0 {
		return nil
	}
	f := float64(done) / float64(total)
	if f > 1 {
		f = 1
	}
	return &f
}
