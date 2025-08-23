package db

import (
	"context"

	dbgen "github.com/Cypherspark/sms-gateway/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
}

func NewDB(pool *pgxpool.Pool) *DB {
	return &DB{Pool: pool, Queries: dbgen.New(pool)}
}

func (db *DB) WithTx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.Queries.WithTx(tx)
	if err := fn(q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
