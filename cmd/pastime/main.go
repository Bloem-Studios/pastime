package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Bloem-Studios/pastime/internal/cache"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/config"
	"github.com/Bloem-Studios/pastime/internal/connections"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/exports"
	"github.com/Bloem-Studios/pastime/internal/httpapi"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/Bloem-Studios/pastime/internal/presence"
	"github.com/Bloem-Studios/pastime/internal/reviewnotices"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	thirdparty "github.com/Bloem-Studios/pastime/third_party"
	"github.com/jackc/pgx/v5/pgxpool"
)

var version = "0.2.0-dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.Error("command failed", "reason", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 1 && args[0] == "licenses" {
		_, err := os.Stdout.Write(thirdparty.Notices)
		return err
	}
	if len(args) == 0 || (args[0] != "serve" && args[0] != "worker" && args[0] != "migrate" && args[0] != "enqueue-probe" && args[0] != "bootstrap-owner" && args[0] != "review-notices") {
		return errors.New("usage: pastime serve|worker|migrate|licenses|enqueue-probe [dedupe-key]|bootstrap-owner [username] (password on stdin)|review-notices check [year]")
	}
	command := args[0]
	withArgument := command == "enqueue-probe" || command == "bootstrap-owner"
	switch {
	case command == "review-notices":
		if len(args) < 2 || len(args) > 3 || args[1] != "check" {
			return errors.New("usage: pastime review-notices check [year]")
		}
	case (withArgument && len(args) != 2) || (!withArgument && len(args) != 1):
		return errors.New("unexpected command arguments")
	}
	c, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if command == "migrate" {
		migrationCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := database.Migrate(migrationCtx, c.DatabaseURL); err != nil {
			return errors.New("migration failed; verify database access and migration permissions")
		}
		logger.Info("schema is current", "version", database.ExpectedVersion())
		return nil
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	pool, err := database.Open(startup, c.DatabaseURL, c.DatabaseMaxConn)
	if err == nil {
		err = database.CheckSchema(startup, pool)
	}
	cancel()
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		return err
	}
	defer pool.Close()
	if command == "bootstrap-owner" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1025))
		if err != nil || len(data) > 1024 {
			return errors.New("read a bounded password from stdin")
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		u, err := identity.New(pool).Bootstrap(ctx, args[1], args[1], password)
		if err != nil {
			return errors.New("owner bootstrap refused; requires an empty installation and a valid username/password")
		}
		logger.Info("owner created", "user_id", u.ID)
		return nil
	}
	reviews := reviewnotices.Sender{Pool: pool, URL: c.GardenNoticesURL, Token: c.GardenNoticesToken}
	if command == "review-notices" {
		year := time.Now().UTC().Year()
		if len(args) == 3 {
			if year, err = strconv.Atoi(args[2]); err != nil || year < 2000 || year > time.Now().UTC().Year() {
				return errors.New("the year must be from 2000 up to this one")
			}
		}
		result, err := reviews.Check(ctx, year)
		if err != nil {
			return err
		}
		logger.Info("year in review dry run; nothing was sent", "year", year, "result", map[string]int(result))
		return nil
	}
	store := jobs.Store{Pool: pool}
	if command == "enqueue-probe" {
		id, err := jobs.Enqueue(ctx, pool, "runtime_probe", args[1], "idempotent", json.RawMessage(`{}`), 3)
		if err != nil {
			return errors.New("could not enqueue runtime probe")
		}
		fmt.Fprintln(os.Stdout, id)
		return nil
	}
	if command == "worker" {
		logger.Info("worker started", "concurrency", c.WorkerCount)
		valkey, err := cache.Open(c.ValkeyURL)
		if err != nil {
			return err
		}
		if valkey != nil {
			defer valkey.Close()
		}
		catalogService := newCatalog(c, pool, cache.Bytes{Client: valkey})
		catalogService.FetchTimeout = 20 * time.Second
		var scheduler sync.WaitGroup
		box, err := sealed.NewKeyring(c.SecretKey, strings.Split(c.PreviousSecretKeys, ",")...)
		if err != nil {
			return err
		}
		scheduler.Go(func() { schedule(ctx, pool, catalogService, importStore(c, pool, catalogService, box), logger) })
		scheduler.Go(func() { finishCheckins(ctx, pool, logger) })
		scheduler.Go(func() { sendReviewNotices(ctx, reviews, logger) })
		notices := notify.Store{Pool: pool, Box: box, AllowPrivate: c.NotifyPrivate}
		node := &presence.Node{Pool: pool, Role: "worker", Version: version, Concurrency: c.WorkerCount, Providers: catalogService.Client}
		scheduler.Go(func() { node.Run(ctx) })
		jobs.Run(ctx, store, c.WorkerCount, map[string]jobs.Handler{
			"runtime_probe":          func(ctx context.Context, payload json.RawMessage) error { return pool.Ping(ctx) },
			catalog.RefreshJob:       catalogService.RefreshHandler,
			catalog.CalendarTitleJob: catalogService.CalendarTitleHandler,
			catalog.CriticJob:        catalogService.CriticHandler,
			library.WarmJob:          (library.Store{Pool: pool, Cache: catalogService.Cache}).WarmHandler,
			connections.ScrobbleJob:  connections.Store{Pool: pool}.Handler(catalogService, library.Store{Pool: pool}),
			imports.Job:              importStore(c, pool, catalogService, box).Handler,
			imports.PushJob:          importStore(c, pool, catalogService, box).PushHandler,
			imports.ListensJob:       importStore(c, pool, catalogService, box).ListensHandler,
			imports.ListSyncJob:      importStore(c, pool, catalogService, box).ListSyncHandler,
			notify.Job:               notices.Handler,
			exports.Job:              exports.Store{Pool: pool, Library: library.Store{Pool: pool}}.Handler,
		}, logger)
		scheduler.Wait()
		logger.Info("worker stopped")
		return nil
	}
	if c.PublicOrigin == "" {
		return errors.New("PUBLIC_ORIGIN is required when LISTEN_ADDR is not a loopback address")
	}
	valkey, err := cache.Open(c.ValkeyURL)
	if err != nil {
		return err
	}
	var cacheProbe httpapi.Probe
	if valkey != nil {
		defer valkey.Close()
		cacheProbe = func(ctx context.Context) error { return valkey.Ping(ctx).Err() }
	}
	catalogService := newCatalog(c, pool, cache.Bytes{Client: valkey})
	box, err := sealed.NewKeyring(c.SecretKey, strings.Split(c.PreviousSecretKeys, ",")...)
	if err != nil {
		return err
	}
	api, err := httpapi.New(func(ctx context.Context) error { return database.CheckSchema(ctx, pool) }, cacheProbe, version, httpapi.Options{Pool: pool, PublicOrigin: c.PublicOrigin, TrustedProxies: c.TrustedProxies, Logger: logger, Catalog: catalogService, Secrets: box, NotifyPrivate: c.NotifyPrivate,
		Services: serviceCredentials(c), ServicesPrivate: c.ServicesPrivate,
		PublicRegistration:        c.PublicRegistration,
		SiloSyncRequestsPerMinute: c.SiloSyncRequestsPerMinute,
		OIDC:                      httpapi.OIDC{Issuer: c.OIDCIssuer, ClientID: c.OIDCClientID, ClientSecret: c.OIDCClientSecret, Name: c.OIDCName, AutoCreate: c.OIDCAutoCreate}})
	if err != nil {
		return errors.New("could not initialize API contract or documentation")
	}
	srv := &http.Server{
		Addr: c.ListenAddr, Handler: api.Router, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	var wg sync.WaitGroup
	node := &presence.Node{Pool: pool, Role: "server", Version: version, Providers: catalogService.Client}
	wg.Go(func() { node.Run(ctx) })
	stopped := make(chan struct{})
	wg.Go(func() {
		select {
		case <-stopped:
			return
		case <-ctx.Done():
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
		}
	})
	logger.Info("HTTP runtime started", "listen", c.ListenAddr, "stage", "preview")
	err = srv.ListenAndServe()
	close(stopped)
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP listener failed")
	}
	return nil
}

func serviceCredentials(c config.Config) imports.Credentials {
	return imports.Credentials{TraktClientID: c.TraktClientID, TraktClientSecret: c.TraktClientSecret, SimklClientID: c.SimklClientID,
		MALClientID: c.MALClientID, LastfmKey: c.LastfmKey, SteamKey: c.SteamKey, BGGToken: c.BGGToken}
}

func importStore(c config.Config, pool *pgxpool.Pool, catalogService *catalog.Service, box *sealed.Box) imports.Store {
	return imports.Store{Pool: pool, Catalog: catalogService, Library: library.Store{Pool: pool}, Box: box, Creds: serviceCredentials(c), AllowPrivate: c.ServicesPrivate}
}

func newCatalog(c config.Config, pool *pgxpool.Pool, cached catalog.Cache) *catalog.Service {
	if c.ValkeyURL == "" {
		cached = nil
	}
	providers, defaults, client := catalog.Providers(catalog.Credentials{
		TMDBKey: c.TMDBKey, IGDBClientID: c.IGDBClientID, IGDBClientSecret: c.IGDBClientSecret, ComicVineKey: c.ComicVineKey,
		BGGToken: c.BGGToken, Language: c.MetadataLanguage, Contact: c.ProviderContact,
		TVDBKey: c.TVDBKey, TVDBPIN: c.TVDBPIN, GCDUsername: c.GCDUsername, GCDPassword: c.GCDPassword,
	})
	return &catalog.Service{OpenCriticKey: c.OpenCriticKey, Pool: pool, Providers: providers, Client: client, Defaults: defaults, Cache: cached}
}

// finishCheckins completes film and episode check-ins whose runtime has passed, once a
// minute. Finishing locks the check-in row, so concurrent workers never finish one twice.
func finishCheckins(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	store := library.Store{Pool: pool}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if n, err := store.FinishDueCheckins(ctx, 200); err != nil {
			if ctx.Err() == nil {
				logger.Error("check-in auto-finish failed")
			}
		} else if n > 0 {
			logger.Info("check-ins finished at the end of their runtime", "count", n)
		}
	}
}

// sendReviewNotices sends the Year in Review notices once they are due (from 27
// December), checking every hour; failed sends are retried on later passes.
func sendReviewNotices(ctx context.Context, sender reviewnotices.Sender, logger *slog.Logger) {
	if !sender.Configured() {
		return
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		result, err := sender.Run(ctx, time.Now(), 500)
		switch {
		case errors.Is(err, reviewnotices.ErrUnauthorized):
			logger.Error("year in review notices refused: the customer system rejected the token")
		case err != nil && ctx.Err() == nil:
			logger.Error("year in review notices failed")
		case len(result) > 0:
			logger.Info("year in review notices sent", "result", map[string]int(result))
		}
	}
}

// siloKeyWarning is how long before a Silo sync key expires the worker reports it.
const siloKeyWarning = 30 * 24 * time.Hour

// schedule enqueues periodic durable work. Every worker may run it; job dedupe keys make
// concurrent schedulers harmless.
func schedule(ctx context.Context, pool *pgxpool.Pool, catalogService *catalog.Service, links imports.Store, logger *slog.Logger) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	keysChecked := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if n, err := catalogService.EnqueueStale(ctx, time.Now()); err != nil {
			if ctx.Err() == nil {
				logger.Error("metadata refresh scheduling failed")
			}
		} else if n > 0 {
			logger.Info("metadata refresh scheduled", "records", n)
		}
		if catalogService.Cache != nil {
			if err := (library.Store{Pool: pool, Cache: catalogService.Cache}).EnqueueWarm(ctx, time.Now()); err != nil && ctx.Err() == nil {
				logger.Error("statistics warming scheduling failed")
			}
		}
		// One release-notice job per UTC day; the dedupe key makes repeats harmless.
		day := time.Now().UTC().Format("2006-01-02")
		payload, _ := json.Marshal(map[string]string{"date": day})
		if _, err := jobs.Enqueue(ctx, pool, notify.Job, "release_notices:"+day, "idempotent", payload, 3); err != nil && ctx.Err() == nil {
			logger.Error("release notice scheduling failed")
		}
		snapshots := exports.Store{Pool: pool, Library: library.Store{Pool: pool}}
		if _, err := snapshots.EnqueueDue(ctx, func(ctx context.Context, key string, payload []byte) error {
			_, err := jobs.Enqueue(ctx, pool, exports.Job, key, "idempotent", payload, 3)
			return err
		}); err != nil && ctx.Err() == nil {
			logger.Error("export scheduling failed")
		}
		if _, err := (bloem.Store{Pool: pool}).SweepCalendar(ctx); err != nil && ctx.Err() == nil {
			logger.Error("calendar staging maintenance failed")
		}
		if _, err := (bloem.Store{Pool: pool}).SweepListSnapshots(ctx, 16); err != nil && ctx.Err() == nil {
			logger.Error("list snapshot maintenance failed")
		}
		if err := (&identity.Store{Pool: pool}).Sweep(ctx); err != nil && ctx.Err() == nil {
			logger.Error("identity maintenance failed")
		}
		if day != keysChecked {
			// Silo keeps no expiry for its Pastime keys; Pastime renews a key while Silo
			// uses it. A key that is about to expire belongs to a connection that stopped
			// syncing, and its profile needs a new key. Counts only, once a day.
			if keys, err := (bloem.Store{Pool: pool}).SyncKeyExpiry(ctx, siloKeyWarning); err != nil {
				if ctx.Err() == nil {
					logger.Error("silo sync key check failed")
				}
			} else {
				keysChecked = day
				if keys.ExpiringSoon > 0 {
					logger.Warn("silo sync keys expire soon", "count", keys.ExpiringSoon, "first", keys.FirstExpiry.UTC().Format(time.DateOnly), "within_days", int(siloKeyWarning.Hours()/24))
				}
				if keys.Expired > 0 {
					logger.Warn("silo sync keys expired while current", "count", keys.Expired)
				}
			}
		}
		if err := (library.Store{Pool: pool}).PruneChanges(ctx); err != nil && ctx.Err() == nil {
			logger.Error("change feed pruning failed")
		}
		if _, err := links.EnqueueListSyncs(ctx, day); err != nil && ctx.Err() == nil {
			logger.Error("list sync scheduling failed")
		}
		if n, err := links.EnqueueDue(ctx); err != nil && ctx.Err() == nil {
			logger.Error("linked service scheduling failed")
		} else if n > 0 {
			logger.Info("linked services scheduled", "services", n)
		}
		timer.Reset(15 * time.Minute)
	}
}
