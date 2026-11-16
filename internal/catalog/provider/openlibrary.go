package provider

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// OpenLibrary covers books and audiobooks without credentials.
type OpenLibrary struct{ Client *Client }

func (OpenLibrary) Name() string    { return "openlibrary" }
func (OpenLibrary) Kinds() []string { return []string{"book", "audiobook"} }

type olDoc struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Authors      []string `json:"author_name"`
	Year         int      `json:"first_publish_year"`
	Cover        int64    `json:"cover_i"`
	Pages        int      `json:"number_of_pages_median"`
	ISBN         []string `json:"isbn"`
	Subjects     []string `json:"subject"`
	Publishers   []string `json:"publisher"`
	RatingsAvg   float64  `json:"ratings_average"`
	EditionCount int      `json:"edition_count"`
}

const olFields = "key,title,author_name,first_publish_year,cover_i,number_of_pages_median,isbn,subject,publisher,ratings_average,edition_count"

func (o OpenLibrary) record(kind string, d olDoc) Record {
	id := strings.TrimPrefix(d.Key, "/works/")
	r := Record{Provider: "openlibrary", Kind: kind, ProviderID: id, Title: d.Title, Credits: d.Authors, ExternalIDs: map[string]string{}, Details: map[string]any{}}
	if d.Year > 0 {
		r.ReleaseDate = date(strconv.Itoa(d.Year))
	}
	if d.Cover > 0 {
		r.ImageURL = "https://covers.openlibrary.org/b/id/" + strconv.FormatInt(d.Cover, 10) + "-L.jpg"
	}
	if kind == "book" {
		r.Total = intPtr(d.Pages)
	}
	if len(d.Subjects) > 8 {
		d.Subjects = d.Subjects[:8]
	}
	r.Genres = d.Subjects
	if d.RatingsAvg > 0 {
		r.Rating = floatPtr(d.RatingsAvg * 2)
	}
	for _, isbn := range d.ISBN {
		if len(isbn) == 13 {
			r.ExternalIDs["isbn"] = isbn
			break
		}
	}
	if len(d.Publishers) > 0 {
		r.Details["publisher"] = clean(d.Publishers[0], 200)
	}
	return r
}

func (o OpenLibrary) search(ctx context.Context, query url.Values) ([]olDoc, error) {
	query.Set("fields", olFields)
	var out struct {
		Docs []olDoc `json:"docs"`
	}
	err := o.Client.JSON(ctx, Request{URL: "https://openlibrary.org/search.json?" + query.Encode(), Retries: 1}, &out)
	return out.Docs, err
}

func (o OpenLibrary) Search(ctx context.Context, kind, query string) ([]Record, error) {
	q := url.Values{"limit": {"20"}}
	// A bare ISBN goes to the ISBN index rather than the title index.
	if digits := strings.ReplaceAll(strings.ReplaceAll(query, "-", ""), " ", ""); (len(digits) == 10 || len(digits) == 13) && strings.Trim(digits, "0123456789Xx") == "" {
		q.Set("isbn", digits)
	} else {
		q.Set("q", query)
	}
	docs, err := o.search(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(docs))
	for _, d := range docs {
		if strings.HasPrefix(d.Key, "/works/") && d.Title != "" {
			out = append(out, o.record(kind, d))
		}
	}
	return out, nil
}

func (o OpenLibrary) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if !strings.HasPrefix(id, "OL") || !strings.HasSuffix(id, "W") || strings.ContainsAny(id, "/?#") {
		return Record{}, ErrNotFound
	}
	docs, err := o.search(ctx, url.Values{"q": {"key:/works/" + id}, "limit": {"1"}})
	if err != nil {
		return Record{}, err
	}
	if len(docs) == 0 {
		return Record{}, ErrNotFound
	}
	r := o.record(kind, docs[0])
	var work struct {
		Description any `json:"description"`
	}
	if err := o.Client.JSON(ctx, Request{URL: "https://openlibrary.org/works/" + id + ".json"}, &work); err == nil {
		switch d := work.Description.(type) {
		case string:
			r.Overview = d
		case map[string]any:
			r.Overview, _ = d["value"].(string)
		}
	}
	return r, nil
}

// Editions lists a work's editions, those with a cover and ISBN first.
func (o OpenLibrary) Editions(ctx context.Context, kind, id string) ([]Edition, error) {
	if !strings.HasPrefix(id, "OL") || !strings.HasSuffix(id, "W") || strings.ContainsAny(id, "/?#") {
		return nil, ErrNotFound
	}
	var out struct {
		Entries []struct {
			Key       string   `json:"key"`
			Title     string   `json:"title"`
			Publisher []string `json:"publishers"`
			Published string   `json:"publish_date"`
			Pages     int      `json:"number_of_pages"`
			ISBN13    []string `json:"isbn_13"`
			ISBN10    []string `json:"isbn_10"`
			Covers    []int64  `json:"covers"`
			Format    string   `json:"physical_format"`
			Languages []struct {
				Key string `json:"key"`
			} `json:"languages"`
		} `json:"entries"`
	}
	if err := o.Client.JSON(ctx, Request{URL: "https://openlibrary.org/works/" + id + "/editions.json?limit=100", Retries: 1}, &out); err != nil {
		return nil, err
	}
	editions := make([]Edition, 0, len(out.Entries))
	for _, e := range out.Entries {
		ed := Edition{ID: strings.TrimPrefix(e.Key, "/books/"), Title: clean(e.Title, 300), Format: clean(e.Format, 60), Year: clean(e.Published, 40)}
		if ed.ID == "" || strings.ContainsAny(ed.ID, "/?#") {
			continue
		}
		if len(e.Publisher) > 0 {
			ed.Publisher = clean(e.Publisher[0], 200)
		}
		if e.Pages > 0 && e.Pages <= 100000 {
			ed.Pages = intPtr(e.Pages)
		}
		switch {
		case len(e.ISBN13) > 0:
			ed.ISBN = e.ISBN13[0]
		case len(e.ISBN10) > 0:
			ed.ISBN = e.ISBN10[0]
		}
		for _, c := range e.Covers {
			if c > 0 {
				ed.ImageURL = "https://covers.openlibrary.org/b/id/" + strconv.FormatInt(c, 10) + "-L.jpg"
				break
			}
		}
		if len(e.Languages) > 0 {
			ed.Language = strings.TrimPrefix(e.Languages[0].Key, "/languages/")
		}
		editions = append(editions, ed)
	}
	sort.SliceStable(editions, func(i, j int) bool { return editionScore(editions[i]) > editionScore(editions[j]) })
	return editions, nil
}

func editionScore(e Edition) int {
	n := 0
	for _, has := range []bool{e.ImageURL != "", e.ISBN != "", e.Pages != nil, e.Publisher != ""} {
		if has {
			n++
		}
	}
	return n
}

// FetchISBN reads only the ISBN index and refuses multiple matching works. The
// requested ISBN is retained rather than substituting a different edition's ISBN.
func (o OpenLibrary) FetchISBN(ctx context.Context, kind, isbn string) (Record, error) {
	if kind != "book" && kind != "audiobook" {
		return Record{}, ErrUnsupported
	}
	isbn = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(isbn), "-", ""), " ", ""))
	if (len(isbn) != 10 && len(isbn) != 13) || strings.Trim(isbn, "0123456789X") != "" || strings.Contains(isbn[:len(isbn)-1], "X") || (len(isbn) == 13 && strings.Contains(isbn, "X")) {
		return Record{}, ErrNotFound
	}
	q := url.Values{"isbn": {isbn}, "limit": {"20"}, "fields": {olFields}}
	var out struct {
		Found int     `json:"numFound"`
		Docs  []olDoc `json:"docs"`
	}
	if err := o.Client.JSON(ctx, Request{URL: "https://openlibrary.org/search.json?" + q.Encode()}, &out); err != nil {
		return Record{}, err
	}
	if out.Found > 20 || (out.Found == 0 && len(out.Docs) >= 20) {
		return Record{}, ErrNotFound
	}
	var match *olDoc
	for i := range out.Docs {
		d := &out.Docs[i]
		id := strings.TrimPrefix(d.Key, "/works/")
		if !strings.HasPrefix(d.Key, "/works/") || !strings.HasPrefix(id, "OL") || !strings.HasSuffix(id, "W") || strings.ContainsAny(id, "/?#") || d.Title == "" {
			continue
		}
		for _, candidate := range d.ISBN {
			if candidate != isbn {
				continue
			}
			if match != nil && match.Key != d.Key {
				return Record{}, ErrNotFound
			}
			match = d
			break
		}
	}
	if match == nil {
		return Record{}, ErrNotFound
	}
	rec := o.record(kind, *match)
	rec.ExternalIDs["isbn"] = isbn
	return rec, nil
}
