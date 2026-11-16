package catalog

import (
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"sort"
	"time"
)

// ProviderSpec is public adapter metadata, never a credential value.
type ProviderSpec struct {
	Name                   string
	Kinds, Hosts, Settings []string
}

// ExpectedProviders also describes adapters omitted from the active registry because
// settings are absent. Active extensions are merged by Inventory and Choices.
var ExpectedProviders = []ProviderSpec{
	{"tmdb", []string{"movie", "series"}, []string{"api.themoviedb.org"}, []string{"TMDB_API_KEY"}},
	{"openlibrary", []string{"book", "audiobook"}, []string{"openlibrary.org"}, nil},
	{"anilist", []string{"anime", "manga"}, []string{"graphql.anilist.co"}, nil},
	{"musicbrainz", []string{"music"}, []string{"musicbrainz.org"}, nil},
	{"itunes", []string{"podcast"}, []string{"itunes.apple.com"}, nil},
	{"igdb", []string{"game"}, []string{"api.igdb.com", "id.twitch.tv"}, []string{"IGDB_CLIENT_ID", "IGDB_CLIENT_SECRET"}},
	{"comicvine", []string{"comic"}, []string{"comicvine.gamespot.com"}, []string{"COMICVINE_API_KEY"}},
	{"bgg", []string{"boardgame"}, []string{"boardgamegeek.com"}, []string{"BGG_TOKEN"}},
	{"tvdb", []string{"series", "anime"}, []string{"api4.thetvdb.com"}, []string{"TVDB_API_KEY", "TVDB_PIN"}},
	{"mangabaka", []string{"manga"}, []string{"api.mangabaka.org"}, nil},
	{"youtube", []string{"video"}, []string{"www.youtube.com"}, nil},
	{"rss", []string{"podcast"}, nil, nil},
	{"gcd", []string{"comic"}, []string{"www.comics.org"}, nil},
}

func (s *Service) Inventory() []ProviderSpec {
	byName := map[string]ProviderSpec{}
	for _, spec := range ExpectedProviders {
		byName[spec.Name] = spec
	}
	for name, p := range s.Providers {
		spec := byName[name]
		spec.Name = name
		spec.Kinds = p.Kinds()
		byName[name] = spec
	}
	out := make([]ProviderSpec, 0, len(byName))
	for _, spec := range byName {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type ProviderChoice struct {
	Name         string                     `json:"name"`
	Kinds        []string                   `json:"kinds"`
	Defaults     []string                   `json:"defaults"`
	Configured   bool                       `json:"configured"`
	Availability string                     `json:"availability"`
	Health       string                     `json:"health"`
	Search       provider.QueryCapabilities `json:"search"`
	Browse       provider.QueryCapabilities `json:"browse"`
	SearchMode   string                     `json:"search_mode"`
}

func (s *Service) Choices() []ProviderChoice {
	out := []ProviderChoice{}
	for _, spec := range s.Inventory() {
		p := s.Providers[spec.Name]
		c := ProviderChoice{Name: spec.Name, Kinds: []string{}, Defaults: []string{}, Configured: p != nil, Availability: "not_configured", Search: provider.QueryCapabilities{Sorts: []string{}}, Browse: provider.QueryCapabilities{Sorts: []string{}}, SearchMode: "keyword", Health: "not_configured"}
		// Capabilities describe implemented behavior even when setup is absent.
		c.Search.Supported = true
		switch spec.Name {
		case "tmdb":
			c.Search, c.Browse = (provider.TMDB{}).QueryCapabilities()
		case "anilist":
			c.Search, c.Browse = (provider.AniList{}).QueryCapabilities()
		case "openlibrary":
			c.Search, c.Browse = (provider.OpenLibrary{}).QueryCapabilities()
		}
		if p != nil {
			c.Availability = "available"
			c.Health = "unknown"
			c.Search = provider.QueryCapabilities{Supported: true, Sorts: []string{}}
			c.Browse = provider.QueryCapabilities{Sorts: []string{}}
			if q, ok := p.(provider.Querier); ok {
				c.Search, c.Browse = q.QueryCapabilities()
			}
			if _, ok := p.(provider.Trender); ok {
				c.Browse.Supported = true
			}
		}
		if p != nil && s.Client != nil {
			var latest provider.HostStats
			for _, h := range s.Client.Stats() {
				for _, host := range spec.Hosts {
					if h.Host == host && h.LastAt.After(latest.LastAt) {
						latest = h
					}
				}
			}
			if !latest.LastAt.IsZero() && time.Since(latest.LastAt) < 15*time.Minute {
				c.Health = "available"
				if latest.LastStatus == 0 || latest.LastStatus == 401 || latest.LastStatus == 403 || latest.LastStatus == 429 || latest.LastStatus >= 500 {
					c.Health = "temporarily_unavailable"
					c.Availability = "temporarily_unavailable"
				}
			}
		}
		if c.Name == "youtube" || c.Name == "rss" {
			c.SearchMode = "address"
		}
		for _, kind := range spec.Kinds {
			if !trackable(kind) {
				continue
			}
			c.Kinds = append(c.Kinds, kind)
			if s.Defaults[kind] == c.Name && c.Configured {
				c.Defaults = append(c.Defaults, kind)
			}
		}
		if len(c.Kinds) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// Availability describes the default adapter's ability to browse. Configuration is
// not a live probe; the current operation reports failures independently.
func (s *Service) Availability(kind string) (string, string) {
	name := s.Defaults[kind]
	for _, c := range s.Choices() {
		if c.Name == name && c.Configured {
			if c.Browse.Supported {
				return name, c.Availability
			}
			return name, "search_only"
		}
	}
	for _, spec := range ExpectedProviders {
		for _, k := range spec.Kinds {
			if k == kind {
				return name, "not_configured"
			}
		}
	}
	return name, "local_only"
}
