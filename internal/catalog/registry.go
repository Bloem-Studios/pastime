package catalog

import (
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/netguard"
)

// Credentials are instance-level provider settings. Keyless providers are always on.
type Credentials struct {
	TMDBKey, IGDBClientID, IGDBClientSecret, ComicVineKey, BGGToken string
	TVDBKey, TVDBPIN, GCDUsername, GCDPassword                      string
	Language, Contact                                               string
}

// Providers builds the adapter set, the default provider for each media type and the
// shared HTTP client whose health administration reports.
// A kind whose provider needs a missing credential falls back to local catalog search.
func Providers(c Credentials) (map[string]provider.Provider, map[string]string, *provider.Client) {
	agent := "Pastime/0.3"
	if c.Contact != "" {
		agent += " (" + c.Contact + ")"
	}
	client := provider.NewClient(agent)
	all := []provider.Provider{
		provider.OpenLibrary{Client: client}, provider.Audnexus{Client: client}, provider.AniList{Client: client},
		provider.MusicBrainz{Client: client}, provider.ITunes{Client: client},
		// Feed addresses are typed by people, so feeds are read through the guarded client.
		provider.RSS{HTTP: netguard.Client(false, 20*time.Second), Agent: agent},
	}
	if c.TMDBKey != "" {
		all = append(all, provider.TMDB{Client: client, Key: c.TMDBKey, Language: c.Language})
	}
	// Listed after the defaults for their kinds, so each is an alternative to choose.
	all = append(all, provider.MangaBaka{Client: client}, provider.YouTube{Client: client})
	if c.TVDBKey != "" {
		all = append(all, provider.NewTVDB(client, c.TVDBKey, c.TVDBPIN))
	}
	if c.IGDBClientID != "" && c.IGDBClientSecret != "" {
		all = append(all, provider.NewIGDB(client, c.IGDBClientID, c.IGDBClientSecret))
	}
	if c.ComicVineKey != "" {
		all = append(all, provider.ComicVine{Client: client, Key: c.ComicVineKey})
	}
	if c.BGGToken != "" {
		all = append(all, provider.BGG{Client: client, Token: c.BGGToken})
	}
	all = append(all, provider.GCD{Client: client, Username: c.GCDUsername, Password: c.GCDPassword})
	providers, defaults := map[string]provider.Provider{}, map[string]string{}
	for _, p := range all {
		providers[p.Name()] = p
		for _, kind := range p.Kinds() {
			if _, taken := defaults[kind]; !taken {
				defaults[kind] = p.Name()
			}
		}
	}
	return providers, defaults, client
}
