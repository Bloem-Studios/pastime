// Package config validates runtime settings without logging their secret values.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Config is immutable after process startup. Each process owns a bounded pool.
type Config struct {
	OpenCriticKey   string
	ListenAddr      string
	PublicOrigin    string
	DatabaseURL     string
	ValkeyURL       string
	DatabaseMaxConn int32
	WorkerCount     int
	// Metadata provider credentials. Secrets are never logged or returned by the API.
	TMDBKey, IGDBClientID, IGDBClientSecret, ComicVineKey, BGGToken string
	TVDBKey, TVDBPIN, GCDUsername, GCDPassword                      string
	MetadataLanguage, ProviderContact                               string
	// SecretKey encrypts stored secrets such as notification targets. Optional; features
	// that need it are unavailable without it.
	SecretKey          string
	PreviousSecretKeys string
	TrustedProxies     string
	// OIDC sign-in, enabled when all three are set.
	OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCName string
	OIDCAutoCreate                                       bool
	PublicRegistration                                   bool
	// NotifyPrivate allows notification destinations on private networks.
	NotifyPrivate bool
	// Application keys for linked services. Each service is offered only when its key is set.
	TraktClientID, TraktClientSecret, SimklClientID, MALClientID, LastfmKey, SteamKey string
	// ServicesPrivate lets members link services on private networks; administrators always may.
	ServicesPrivate bool
	// SiloSyncRequestsPerMinute limits each Silo sync credential (ptbs_) on its own.
	SiloSyncRequestsPerMinute int
	// ReviewNoticesURL and ReviewNoticesToken let the worker ask the customer system
	// (the customer-notice service) to email each customer their Year in Review. Both or neither.
	ReviewNoticesURL, ReviewNoticesToken string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		ListenAddr: "127.0.0.1:8080", DatabaseURL: getenv("DATABASE_URL"),
		ValkeyURL: getenv("VALKEY_URL"), DatabaseMaxConn: 8, WorkerCount: 2,
		SiloSyncRequestsPerMinute: 600,
	}
	c.OpenCriticKey = getenv("OPENCRITIC_API_KEY")
	c.TMDBKey, c.IGDBClientID, c.IGDBClientSecret = getenv("TMDB_API_KEY"), getenv("IGDB_CLIENT_ID"), getenv("IGDB_CLIENT_SECRET")
	c.ComicVineKey, c.BGGToken, c.ProviderContact = getenv("COMICVINE_API_KEY"), getenv("BGG_TOKEN"), getenv("PROVIDER_CONTACT")
	c.TVDBKey, c.TVDBPIN = getenv("TVDB_API_KEY"), getenv("TVDB_PIN")
	c.GCDUsername, c.GCDPassword = getenv("GCD_USERNAME"), getenv("GCD_PASSWORD")
	c.TrustedProxies = getenv("TRUSTED_PROXY_CIDRS")
	c.SecretKey = getenv("SECRET_KEY")
	c.PreviousSecretKeys = getenv("SECRET_KEY_PREVIOUS")
	c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret, c.OIDCName = getenv("OIDC_ISSUER"), getenv("OIDC_CLIENT_ID"), getenv("OIDC_CLIENT_SECRET"), getenv("OIDC_NAME")
	c.OIDCAutoCreate = getenv("OIDC_AUTO_CREATE") == "true"
	c.PublicRegistration = getenv("PUBLIC_REGISTRATION") == "true"
	c.NotifyPrivate = getenv("NOTIFY_ALLOW_PRIVATE") == "true"
	c.TraktClientID, c.TraktClientSecret, c.SimklClientID = getenv("TRAKT_CLIENT_ID"), getenv("TRAKT_CLIENT_SECRET"), getenv("SIMKL_CLIENT_ID")
	c.MALClientID, c.LastfmKey, c.SteamKey = getenv("MAL_CLIENT_ID"), getenv("LASTFM_API_KEY"), getenv("STEAM_API_KEY")
	c.ServicesPrivate = getenv("SERVICES_ALLOW_PRIVATE") == "true"
	c.MetadataLanguage = getenv("METADATA_LANGUAGE")
	if c.MetadataLanguage == "" {
		c.MetadataLanguage = "en-US"
	}
	if v := getenv("LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return Config{}, errors.New("LISTEN_ADDR must be host:port")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" || u.Path == "/" {
		return Config{}, errors.New("DATABASE_URL must be a PostgreSQL URL with a database name")
	}
	if c.ValkeyURL != "" {
		u, err = url.Parse(c.ValkeyURL)
		if err != nil || u == nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" {
			return Config{}, errors.New("VALKEY_URL must use redis:// or rediss:// with a hostname")
		}
	}
	for _, field := range []struct {
		key      string
		min, max int
		set      func(int)
	}{
		{"DATABASE_MAX_CONNECTIONS", 2, 64, func(n int) { c.DatabaseMaxConn = int32(n) }},
		{"WORKER_COUNT", 1, 16, func(n int) { c.WorkerCount = n }},
		{"SILO_SYNC_REQUESTS_PER_MINUTE", 60, 100000, func(n int) { c.SiloSyncRequestsPerMinute = n }},
	} {
		if v := getenv(field.key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < field.min || n > field.max {
				return Config{}, fmt.Errorf("%s must be between %d and %d", field.key, field.min, field.max)
			}
			field.set(n)
		}
	}
	if c.PublicOrigin = getenv("PUBLIC_ORIGIN"); c.PublicOrigin != "" {
		if c.PublicOrigin, err = NormalizeOrigin(c.PublicOrigin); err != nil {
			return Config{}, err
		}
	} else {
		// Only a loopback listener implies a usable browser origin. Other listeners
		// leave it empty; serve refuses to start without an explicit PUBLIC_ORIGIN.
		c.PublicOrigin = defaultOrigin(c.ListenAddr)
	}
	c.ReviewNoticesURL, c.ReviewNoticesToken = getenv("REVIEW_NOTICES_URL"), getenv("REVIEW_NOTICES_TOKEN")
	if (c.ReviewNoticesURL == "") != (c.ReviewNoticesToken == "") {
		return Config{}, errors.New("set both REVIEW_NOTICES_URL and REVIEW_NOTICES_TOKEN, or neither")
	}
	if c.ReviewNoticesURL != "" {
		u, err := url.Parse(c.ReviewNoticesURL)
		if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Config{}, errors.New("REVIEW_NOTICES_URL must be an https URL without credentials or query")
		}
	}
	if c.WorkerCount > int(c.DatabaseMaxConn)-1 {
		return Config{}, errors.New("WORKER_COUNT must leave at least one database connection free")
	}
	return c, nil
}

// HTTP is restricted to local development; non-loopback browser origins require HTTPS.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSuffix(raw, "/")
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || raw != u.Scheme+"://"+u.Host {
		return "", errors.New("PUBLIC_ORIGIN must be an HTTP(S) origin without credentials, path or query")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("PUBLIC_ORIGIN requires HTTPS outside loopback development")
	}
	return raw, nil
}

func defaultOrigin(listen string) string {
	host, port, _ := net.SplitHostPort(listen)
	origin, err := NormalizeOrigin("http://" + net.JoinHostPort(host, port))
	if err != nil {
		return ""
	}
	return origin
}
