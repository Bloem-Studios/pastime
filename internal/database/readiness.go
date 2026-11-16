package database

import (
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ExpectedVersion() int64 {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		panic("invalid embedded migration layout")
	}
	var latest int64
	for _, name := range entries {
		prefix, _, ok := strings.Cut(name, "_")
		n, err := strconv.ParseInt(prefix, 10, 64)
		if !ok || err != nil {
			panic("invalid embedded migration filename")
		}
		if n > latest {
			latest = n
		}
	}
	return latest
}

func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var current int64
	if err := pool.QueryRow(ctx, `SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1`).Scan(&current); err != nil {
		return errors.New("database schema is unavailable")
	}
	if current != ExpectedVersion() {
		return errors.New("database schema version does not match this application")
	}
	return nil
}
