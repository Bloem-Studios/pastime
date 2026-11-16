package provider

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (TMDB) QueryCapabilities() (QueryCapabilities, QueryCapabilities) {
	return QueryCapabilities{Supported: true, Pagination: true, Year: true, Sorts: []string{}}, QueryCapabilities{Supported: true, Pagination: true, Year: true, Genre: true, Sorts: []string{"popular", "rating", "newest"}}
}
func (t TMDB) Query(ctx context.Context, kind string, o QueryOptions) (RecordPage, error) {
	k := map[string]string{"movie": "movie", "series": "tv"}[kind]
	if k == "" {
		return RecordPage{}, ErrUnsupported
	}
	page := max(o.Page, 1)
	q := url.Values{"page": {strconv.Itoa(page)}, "include_adult": {"false"}}
	endpoint := "/discover/" + k
	if o.Query != "" {
		endpoint = "/search/" + k
		q.Set("query", o.Query)
	}
	if o.Year > 0 {
		field := "primary_release_year"
		if k == "tv" {
			field = "first_air_date_year"
		}
		if o.Query != "" && k == "movie" {
			field = "year"
		}
		q.Set(field, strconv.Itoa(o.Year))
	}
	if o.Genre != "" {
		q.Set("with_genres", o.Genre)
	}
	if o.Query == "" {
		sortBy := map[string]string{"": "popularity.desc", "popular": "popularity.desc", "rating": "vote_average.desc", "newest": "primary_release_date.desc"}[o.Sort]
		if sortBy == "" {
			return RecordPage{}, ErrUnsupported
		}
		if k == "tv" && o.Sort == "newest" {
			sortBy = "first_air_date.desc"
		}
		q.Set("sort_by", sortBy)
		if o.Sort == "rating" {
			q.Set("vote_count.gte", "100")
		}
	}
	var resp struct {
		Page       int        `json:"page"`
		TotalPages int        `json:"total_pages"`
		Results    []tmdbItem `json:"results"`
	}
	if err := t.get(ctx, endpoint, q, &resp); err != nil {
		return RecordPage{}, err
	}
	out := RecordPage{Items: []Record{}}
	for _, v := range resp.Results {
		out.Items = append(out.Items, t.record(kind, v))
	}
	if page < resp.TotalPages && page < 500 {
		out.NextPage = page + 1
	}
	return out, nil
}

func (AniList) QueryCapabilities() (QueryCapabilities, QueryCapabilities) {
	return QueryCapabilities{Supported: true, Pagination: true, Year: true, Genre: true, Sorts: []string{}}, QueryCapabilities{Supported: true, Pagination: true, Year: true, Genre: true, Sorts: []string{"popular", "rating", "newest"}}
}
func (a AniList) Query(ctx context.Context, kind string, o QueryOptions) (RecordPage, error) {
	k, err := aniType(kind)
	if err != nil {
		return RecordPage{}, err
	}
	page := max(o.Page, 1)
	vars := map[string]any{"t": k, "page": page}
	declarations, filters := "$t:MediaType,$page:Int", "type:$t,isAdult:false"
	if o.Query != "" {
		vars["q"] = o.Query
		declarations += ",$q:String"
		filters += ",search:$q"
	}
	if o.Year > 0 {
		filters += ",startDate_like:$yearString"
		declarations += ",$yearString:String"
		vars["yearString"] = fmt.Sprintf("%04d%%", o.Year)
	}
	if o.Genre != "" {
		vars["genre"] = o.Genre
		declarations += ",$genre:String"
		filters += ",genre:$genre"
	}
	sortBy := map[string]string{"": "POPULARITY_DESC", "popular": "POPULARITY_DESC", "rating": "SCORE_DESC", "newest": "START_DATE_DESC"}[o.Sort]
	if o.Query != "" {
		sortBy = "SEARCH_MATCH"
	}
	if sortBy == "" {
		return RecordPage{}, ErrUnsupported
	}
	var resp struct {
		Errors []any `json:"errors"`
		Data   struct {
			Page *struct {
				Info struct {
					HasNext bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []aniMedia `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	query := `query(` + declarations + `){Page(page:$page,perPage:20){pageInfo{hasNextPage} media(` + filters + `,sort:` + sortBy + `){` + aniFields + `}}}`
	if err := a.query(ctx, query, vars, &resp); err != nil {
		return RecordPage{}, err
	}
	if len(resp.Errors) > 0 || resp.Data.Page == nil {
		return RecordPage{}, ErrUnavailable
	}
	out := RecordPage{Items: []Record{}}
	for _, v := range resp.Data.Page.Media {
		out.Items = append(out.Items, a.record(kind, v))
	}
	if resp.Data.Page.Info.HasNext && page < 500 {
		out.NextPage = page + 1
	}
	return out, nil
}
func (a AniList) Trending(ctx context.Context, kind string) ([]Record, error) {
	page, err := a.Query(ctx, kind, QueryOptions{Sort: "popular"})
	return page.Items, err
}

func (OpenLibrary) QueryCapabilities() (QueryCapabilities, QueryCapabilities) {
	return QueryCapabilities{Supported: true, Pagination: true, Year: true, Sorts: []string{}}, QueryCapabilities{Sorts: []string{}}
}
func (o OpenLibrary) Query(ctx context.Context, kind string, opts QueryOptions) (RecordPage, error) {
	if (kind != "book" && kind != "audiobook") || opts.Query == "" {
		return RecordPage{}, ErrUnsupported
	}
	page := max(opts.Page, 1)
	q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "fields": {olFields}}
	query := opts.Query
	if opts.Year > 0 {
		query = "(" + query + ") AND first_publish_year:" + strconv.Itoa(opts.Year)
	}
	q.Set("q", query)
	digits := strings.ReplaceAll(strings.ReplaceAll(opts.Query, "-", ""), " ", "")
	if opts.Year == 0 && (len(digits) == 10 || len(digits) == 13) && strings.Trim(digits, "0123456789Xx") == "" {
		q.Del("q")
		q.Set("isbn", digits)
	}
	var resp struct {
		Start int     `json:"start"`
		Found int     `json:"numFound"`
		Docs  []olDoc `json:"docs"`
	}
	if err := o.Client.JSON(ctx, Request{URL: "https://openlibrary.org/search.json?" + q.Encode(), Retries: 1}, &resp); err != nil {
		return RecordPage{}, err
	}
	out := RecordPage{Items: []Record{}}
	for _, d := range resp.Docs {
		if strings.HasPrefix(d.Key, "/works/") && d.Title != "" {
			out.Items = append(out.Items, o.record(kind, d))
		}
	}
	if page*20 < resp.Found && page < 500 {
		out.NextPage = page + 1
	}
	return out, nil
}
