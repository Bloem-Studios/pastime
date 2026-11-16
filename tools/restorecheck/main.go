// restorecheck verifies a disposable restored database without activating workers,
// providers, notification delivery or outgoing synchronization.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func check(ctx context.Context, dsn, keyFile string) (map[string]int, error) {
	out := map[string]int{}
	u, err := url.Parse(dsn)
	if err != nil {
		return out, errors.New("invalid rehearsal database")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || u.Path != "/pastime_restore_check" {
		return out, errors.New("refusing database outside loopback pastime_restore_check")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "pastime_restore_check" {
		return out, errors.New("invalid rehearsal database configuration")
	}
	for k := range u.Query() {
		if k != "sslmode" {
			return out, errors.New("unexpected database URL parameter")
		}
	}
	file, err := os.Open(keyFile)
	if err != nil {
		return out, errors.New("cannot read recovery key file")
	}
	defer file.Close()
	keys := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok && (key == "SECRET_KEY" || key == "SECRET_KEY_PREVIOUS") {
			keys[key] = value
		}
	}
	if scanner.Err() != nil {
		return out, errors.New("cannot read recovery keys")
	}
	box, err := sealed.NewKeyring(keys["SECRET_KEY"], strings.Split(keys["SECRET_KEY_PREVIOUS"], ",")...)
	if err != nil {
		return out, errors.New("invalid recovery keyring")
	}
	pool, err := database.Open(ctx, dsn, 2)
	if err != nil {
		return out, err
	}
	defer pool.Close()
	if err = database.CheckSchema(ctx, pool); err != nil {
		return out, err
	}
	for _, table := range []string{"users", "library_entries", "plays", "owned_copies", "personal_lists"} {
		var n int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			return out, errors.New("restored tables are unreadable")
		}
		out[table] = n
	}
	if err = checkSecrets(ctx, pool, box, out); err != nil {
		return out, err
	}
	return out, nil
}

func checkSecrets(ctx context.Context, pool *pgxpool.Pool, box *sealed.Box, out map[string]int) error {
	for _, spec := range [][3]string{{"notification_targets", "sealed_config", "notify-target:"}, {"service_links", "sealed_config", "service-link:"}, {"user_totp", "sealed_secret", "totp:"}, {"service_authorizations", "sealed_state", "service-auth:"}} {
		rows, err := pool.Query(ctx, "SELECT user_id,"+spec[1]+" FROM "+spec[0])
		if err != nil {
			return errors.New("restored secrets are unreadable")
		}
		for rows.Next() {
			var id int64
			var ciphertext []byte
			if err = rows.Scan(&id, &ciphertext); err != nil {
				rows.Close()
				return errors.New("invalid restored secret row")
			}
			plain, err := box.Open(ciphertext, spec[2]+strconv.FormatInt(id, 10))
			if err != nil {
				rows.Close()
				return errors.New("a restored secret cannot be decrypted")
			}
			clear(plain)
			out["secrets_opened"]++
		}
		rows.Close()
		if rows.Err() != nil {
			return errors.New("restored secret scan failed")
		}
	}
	return nil
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: restorecheck /path/to/keys.env")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := check(ctx, os.Getenv("RESTORE_DATABASE_URL"), os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
