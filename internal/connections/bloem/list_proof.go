package bloem

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// This independently authored source signer implements the owned lp1 wire
// contract. Only the public key travels through the installation broker.
func (s Store) listSigningKeyTx(ctx context.Context, tx pgx.Tx, instance string) (ed25519.PrivateKey, error) {
	if s.Box == nil {
		return nil, errors.New("list signer unavailable")
	}
	var sealed []byte
	err := tx.QueryRow(ctx, `SELECT seed_cipher FROM bloem_list_signing_keys WHERE instance_id=$1`, instance).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		seed := make([]byte, ed25519.SeedSize)
		if _, err = rand.Read(seed); err != nil {
			return nil, err
		}
		sealed, err = s.Box.Seal(seed, "bloem.list.signing.v1:"+instance)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_list_signing_keys(instance_id,seed_cipher) VALUES($1,$2) ON CONFLICT(instance_id) DO NOTHING`, instance, sealed); err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, `SELECT seed_cipher FROM bloem_list_signing_keys WHERE instance_id=$1`, instance).Scan(&sealed)
	}
	if err != nil {
		return nil, err
	}
	seed, err := s.Box.Open(sealed, "bloem.list.signing.v1:"+instance)
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid list signer")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s Store) signListProofTx(ctx context.Context, tx pgx.Tx, in ListSnapshotInput, page ListSnapshotPage) (string, error) {
	key, err := s.listSigningKeyTx(ctx, tx, in.InstanceID)
	if err != nil {
		return "", err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", err
	}
	claims := struct {
		Version    int       `json:"version"`
		InstanceID string    `json:"instance_id"`
		AccountID  string    `json:"account_id"`
		ProfileID  string    `json:"profile_id"`
		SourceID   string    `json:"source_id"`
		SnapshotID string    `json:"snapshot_id"`
		Digest     string    `json:"digest"`
		IssuedAt   time.Time `json:"issued_at"`
		ExpiresAt  time.Time `json:"expires_at"`
	}{1, in.InstanceID, in.AccountID, in.ProfileID, page.SourceID, page.SnapshotID, page.Digest, now, page.ExpiresAt}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(key, append([]byte("bloem.list.proof.v1\x00"), body...))
	return "lp1." + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
