package database

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"math"
	"net/url"
	"time"

	"github.com/Bloem-Studios/pastime/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func Open(ctx context.Context, dsn string, maxConnections int32) (*pgxpool.Pool, error) {
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	c.MaxConns = maxConnections
	c.MinConns = 0
	c.MaxConnLifetime = 30 * time.Minute
	c.MaxConnLifetimeJitter = 5 * time.Minute
	c.MaxConnIdleTime = 5 * time.Minute
	c.ConnConfig.ConnectTimeout = 3 * time.Second
	c.ConnConfig.RuntimeParams["application_name"] = "pastime"
	c.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	c.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	c.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10000"
	pool, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, errors.New("could not create database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database is unavailable")
	}
	return pool, nil
}

// Migrate is an explicit maintenance operation, not an API startup side effect.
func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid migration database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// Migrations of different schemas (such as isolated test schemas) do not contend
	// for one database-wide lock; the default schema keeps goose's usual lock.
	var options []lock.SessionLockerOption
	if u, err := url.Parse(dsn); err == nil {
		if path := u.Query().Get("search_path"); path != "" {
			h := fnv.New64a()
			_, _ = h.Write([]byte(path))
			options = append(options, lock.WithLockID(int64(h.Sum64()&math.MaxInt64)))
		}
	}
	locker, err := lock.NewPostgresSessionLocker(options...)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
