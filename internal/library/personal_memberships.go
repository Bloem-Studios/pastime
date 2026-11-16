package library

import (
	"context"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type PersonalMemberships struct {
	Favourite bool `json:"favourite"`
	Watchlist bool `json:"watchlist"`
}
type PersonalMembershipInput struct {
	Kind    string `json:"kind"`
	Present bool   `json:"present"`
}

func (s Store) PersonalMemberships(ctx context.Context, a identity.Actor, catalogID int64) (PersonalMemberships, error) {
	var out PersonalMemberships
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	a, err = identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return out, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND parent_id IS NULL AND kind IN ('movie','series'))`, catalogID).Scan(&valid); err != nil {
		return out, err
	}
	if !valid {
		return out, fault.NotFound
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(bool_or(present) FILTER(WHERE kind='favorite'),false),COALESCE(bool_or(present) FILTER(WHERE kind='watchlist'),false) FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2`, a.TrackingID(), catalogID).Scan(&out.Favourite, &out.Watchlist)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s Store) SetMembership(ctx context.Context, a identity.Actor, key string, catalogID int64, in PersonalMembershipInput) error {
	if catalogID < 1 || in.Kind != "favorite" && in.Kind != "watchlist" {
		return fault.Invalid("Choose a favourite or watchlist membership.")
	}
	_, err := s.mutate(ctx, a, "library:write", key, "personal."+in.Kind+"/"+strconv.FormatInt(catalogID, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND parent_id IS NULL AND kind IN ('movie','series'))`, catalogID).Scan(&valid); err != nil {
			return Mutation{}, err
		}
		if !valid {
			return Mutation{}, fault.NotFound
		}
		_, err := s.ApplyPersonalStateTx(ctx, tx, a, catalogID, PersonalState{Kind: in.Kind, Present: in.Present}, time.Now().UTC(), "user:personal."+in.Kind)
		return Mutation{ID: catalogID}, err
	})
	return err
}
