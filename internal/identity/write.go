package identity

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Write keeps fresh credential authorization and a service's database effects in
// one transaction. Callbacks must use tx for database work and perform no network I/O.
func Write[T any](ctx context.Context, pool *pgxpool.Pool, a Actor, scope string, apply func(pgx.Tx, Actor) (T, error)) (T, error) {
	var zero T
	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	a, err = AuthorizeTx(ctx, tx, a, scope)
	if err != nil {
		return zero, err
	}
	out, err := apply(tx, a)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func WriteOnly(ctx context.Context, pool *pgxpool.Pool, a Actor, scope string, apply func(pgx.Tx, Actor) error) error {
	_, err := Write(ctx, pool, a, scope, func(tx pgx.Tx, fresh Actor) (struct{}, error) { return struct{}{}, apply(tx, fresh) })
	return err
}
