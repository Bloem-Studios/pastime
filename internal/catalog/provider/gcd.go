package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// GCD is the Grand Comics Database, a volunteer index of printed comics. Its API
// answers anonymously; configured account credentials are sent when present.
type GCD struct {
	Client             *Client
	Username, Password string
}

func (GCD) Name() string    { return "gcd" }
func (GCD) Kinds() []string { return []string{"comic"} }

const gcdAPI = "https://www.comics.org/api"

var gcdSeriesURL = regexp.MustCompile(`/api/series/([0-9]{1,12})/`)

type gcdSeries struct {
	APIURL       string   `json:"api_url"`
	Name         string   `json:"name"`
	YearBegan    *int     `json:"year_began"`
	YearEnded    *int     `json:"year_ended"`
	Country      string   `json:"country"`
	Language     string   `json:"language"`
	Format       string   `json:"publishing_format"`
	Notes        string   `json:"notes"`
	ActiveIssues []string `json:"active_issues"`
}

func (g GCD) get(ctx context.Context, path string, out any) error {
	req := Request{URL: gcdAPI + path, Retries: 1}
	if g.Username != "" && g.Password != "" {
		req.Header = http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(g.Username+":"+g.Password))}}
	}
	return g.Client.JSON(ctx, req, out)
}

func (g GCD) record(v gcdSeries) (Record, bool) {
	m := gcdSeriesURL.FindStringSubmatch(v.APIURL)
	if m == nil || v.Name == "" {
		return Record{}, false
	}
	r := Record{Provider: "gcd", Kind: "comic", ProviderID: m[1], Title: v.Name, Unit: "items", Overview: clean(v.Notes, 4000),
		ExternalIDs: map[string]string{}, Details: map[string]any{}}
	if v.YearBegan != nil && *v.YearBegan > 0 {
		r.ReleaseDate = date(strconv.Itoa(*v.YearBegan))
	}
	if v.YearEnded != nil && *v.YearEnded > 0 {
		r.ReleaseStatus = "ended"
	}
	if n := len(v.ActiveIssues); n > 0 {
		r.Total = &n
	}
	for key, value := range map[string]string{"country": v.Country, "language": v.Language, "format": v.Format} {
		if value != "" {
			r.Details[key] = clean(value, 80)
		}
	}
	return r, true
}

func (g GCD) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "comic" {
		return nil, ErrUnsupported
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []Record{}, nil
	}
	var resp struct {
		Results []gcdSeries `json:"results"`
	}
	if err := g.get(ctx, "/series/name/"+url.PathEscape(query)+"/?format=json", &resp); err != nil {
		return nil, err
	}
	out := []Record{}
	for _, v := range resp.Results {
		if r, ok := g.record(v); ok && len(out) < 20 {
			out = append(out, r)
		}
	}
	return out, nil
}

// Fetch reads a series and, for its artwork and publisher, its first issue.
func (g GCD) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "comic" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	var v gcdSeries
	if err := g.get(ctx, "/series/"+id+"/?format=json", &v); err != nil {
		return Record{}, err
	}
	r, ok := g.record(v)
	if !ok {
		return Record{}, ErrInvalidPayload
	}
	if len(v.ActiveIssues) > 0 {
		if u, err := url.Parse(v.ActiveIssues[0]); err == nil && strings.HasPrefix(u.Path, "/api/issue/") {
			var issue struct {
				Cover     string `json:"cover"`
				Publisher string `json:"indicia_publisher"`
			}
			if g.get(ctx, strings.TrimPrefix(u.Path, "/api")+"?format=json", &issue) == nil {
				r.ImageURL = safeURL(issue.Cover)
				if issue.Publisher != "" {
					r.Credits = []string{clean(issue.Publisher, 200)}
				}
			}
		}
	}
	return r, nil
}
