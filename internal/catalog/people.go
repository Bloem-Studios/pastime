package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
)

// PersonPage is a cast or crew member with their credits, best known first.
type PersonPage struct {
	Provider  string         `json:"provider"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Biography string         `json:"biography"`
	ImageURL  string         `json:"image_url"`
	KnownFor  string         `json:"known_for"`
	Birthday  *string        `json:"birthday"`
	Deathday  *string        `json:"deathday"`
	Credits   []CreditResult `json:"credits"`
}

type CreditResult struct {
	Result
	Role string `json:"role"`
}

// StudioPage is a production company or network with its popular titles.
type StudioPage struct {
	Provider    string   `json:"provider"`
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	LogoURL     string   `json:"logo_url"`
	Country     string   `json:"country"`
	Titles      []Result `json:"titles"`
}

// cached runs fetch once per key across concurrent callers and keeps the result in the
// shared cache.
func cached[T any](s *Service, ctx context.Context, key string, ttl time.Duration, fetch func(context.Context) (T, error)) (T, error) {
	var out T
	if s.Cache != nil {
		if data, hit := s.Cache.Get(ctx, key); hit && json.Unmarshal(data, &out) == nil {
			return out, nil
		}
	}
	result := s.group.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.fetchTimeout())
		defer cancel()
		value, err := fetch(fetchCtx)
		if err != nil {
			return nil, err
		}
		if s.Cache != nil {
			if data, err := json.Marshal(value); err == nil {
				s.Cache.Set(fetchCtx, key, data, ttl)
			}
		}
		return value, nil
	})
	select {
	case <-ctx.Done():
		return out, ctx.Err()
	case v := <-result:
		if v.Err != nil {
			return out, providerFault(v.Err)
		}
		return v.Val.(T), nil
	}
}

func resultOf(r provider.Record) Result {
	return Result{Provider: r.Provider, ProviderID: r.ProviderID, MediaType: r.Kind, Title: r.Title, Year: r.Year(), ImageURL: r.ImageURL, Overview: truncate(r.Overview, 400), Credits: first(r.Credits, 3)}
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// annotate fills CatalogID for results already in the shared catalog.
func (s *Service) annotate(ctx context.Context, items []*Result) error {
	if len(items) == 0 {
		return nil
	}
	providers, kinds, ids := make([]string, len(items)), make([]string, len(items)), make([]string, len(items))
	for i, r := range items {
		providers[i], kinds[i], ids[i] = r.Provider, r.MediaType, r.ProviderID
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.provider,c.kind,c.provider_id,c.id FROM unnest($1::text[],$2::text[],$3::text[]) AS k(provider,kind,provider_id)
		JOIN catalog_items c ON c.provider=k.provider AND c.kind=k.kind AND c.provider_id=k.provider_id`, providers, kinds, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	known := map[[3]string]int64{}
	for rows.Next() {
		var p, k, pid string
		var id int64
		if err := rows.Scan(&p, &k, &pid, &id); err != nil {
			return err
		}
		known[[3]string{p, k, pid}] = id
	}
	for _, r := range items {
		if id, ok := known[[3]string{r.Provider, r.MediaType, r.ProviderID}]; ok {
			r.CatalogID = &id
		}
	}
	return rows.Err()
}

func (s *Service) directory(name string) (provider.Directory, error) {
	d, ok := s.Providers[name].(provider.Directory)
	if !ok {
		return nil, fault.NotFound
	}
	return d, nil
}

// Person returns a person page, cached for a day.
func (s *Service) Person(ctx context.Context, providerName, id string) (PersonPage, error) {
	d, err := s.directory(providerName)
	if err != nil {
		return PersonPage{}, err
	}
	if len(id) == 0 || len(id) > 40 {
		return PersonPage{}, fault.NotFound
	}
	p, err := cached(s, ctx, "catalog:person:v1:"+providerName+":"+id, 24*time.Hour, func(ctx context.Context) (provider.Person, error) { return d.Person(ctx, id) })
	if err != nil {
		return PersonPage{}, err
	}
	out := PersonPage{Provider: p.Provider, ID: p.ID, Name: p.Name, Biography: p.Biography, ImageURL: p.ImageURL, KnownFor: p.KnownFor,
		Birthday: dateString(p.Birthday), Deathday: dateString(p.Deathday), Credits: make([]CreditResult, 0, len(p.Credits))}
	refs := make([]*Result, 0, len(p.Credits))
	for _, c := range p.Credits {
		out.Credits = append(out.Credits, CreditResult{Result: resultOf(c.Record), Role: c.Role})
	}
	for i := range out.Credits {
		refs = append(refs, &out.Credits[i].Result)
	}
	return out, s.annotate(ctx, refs)
}

// Studio returns a studio page, cached for a day.
func (s *Service) Studio(ctx context.Context, providerName, kind, id string) (StudioPage, error) {
	d, err := s.directory(providerName)
	if err != nil {
		return StudioPage{}, err
	}
	if len(id) == 0 || len(id) > 40 {
		return StudioPage{}, fault.NotFound
	}
	st, err := cached(s, ctx, "catalog:studio:v1:"+providerName+":"+kind+":"+id, 24*time.Hour, func(ctx context.Context) (provider.Studio, error) { return d.Studio(ctx, kind, id) })
	if err != nil {
		return StudioPage{}, err
	}
	out := StudioPage{Provider: st.Provider, Kind: st.Kind, ID: st.ID, Name: st.Name, Description: st.Description, LogoURL: st.LogoURL, Country: st.Country, Titles: make([]Result, 0, len(st.Titles))}
	for _, r := range st.Titles {
		out.Titles = append(out.Titles, resultOf(r))
	}
	refs := make([]*Result, len(out.Titles))
	for i := range out.Titles {
		refs[i] = &out.Titles[i]
	}
	return out, s.annotate(ctx, refs)
}

// Trending lists popular titles of a kind from its default provider, cached for six
// hours. Kinds whose provider has no trending list return nothing.
func (s *Service) Trending(ctx context.Context, kind string) ([]Result, error) {
	name := s.Defaults[kind]
	t, ok := s.Providers[name].(provider.Trender)
	if !ok {
		return []Result{}, nil
	}
	records, err := cached(s, ctx, "catalog:trending:v1:"+name+":"+kind, 6*time.Hour, func(ctx context.Context) ([]provider.Record, error) {
		found, err := t.Trending(ctx, kind)
		if err != nil {
			return nil, err
		}
		for i := range found {
			found[i].Children = nil
		}
		return found, nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(records))
	for _, r := range records {
		out = append(out, resultOf(r))
	}
	refs := make([]*Result, len(out))
	for i := range out {
		refs[i] = &out[i]
	}
	return out, s.annotate(ctx, refs)
}

// Edition is one printing or recording of a catalogued work.
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

// Editions lists a catalogued work's editions, cached for a day. Providers without
// editions return none.
func (s *Service) Editions(ctx context.Context, catalogID int64) ([]Edition, error) {
	item, err := s.Get(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	lister, ok := s.Providers[item.Provider].(provider.EditionLister)
	if !ok || item.ParentID != nil {
		return []Edition{}, nil
	}
	found, err := cached(s, ctx, "catalog:editions:v1:"+item.Provider+":"+item.Kind+":"+item.ProviderID, 24*time.Hour, func(ctx context.Context) ([]provider.Edition, error) {
		return lister.Editions(ctx, item.Kind, item.ProviderID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Edition, 0, len(found))
	for _, e := range found {
		out = append(out, Edition{ID: e.ID, Title: e.Title, Publisher: e.Publisher, Year: e.Year, Pages: e.Pages, ISBN: e.ISBN, Format: e.Format, Language: e.Language, ImageURL: e.ImageURL})
	}
	return out, nil
}
