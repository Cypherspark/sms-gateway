package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type dbEnv struct {
	Pool *pgxpool.Pool
	Term func()
}

func startPostgres(t *testing.T) *dbEnv {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		Env:          map[string]string{"POSTGRES_USER": "sms", "POSTGRES_PASSWORD": "sms", "POSTGRES_DB": "sms"},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	pg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil { t.Fatalf("start container: %v", err) }

	host, _ := pg.Host(ctx)
	port, _ := pg.MappedPort(ctx, "5432")
	dsn := "postgres://sms:sms@" + host + ":" + port.Port() + "/sms?sslmode=disable"

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil { t.Fatalf("pgxpool: %v", err) }

	applyMigrations(t, ctx, pool)

	return &dbEnv{Pool: pool, Term: func() { pool.Close(); _ = pg.Terminate(ctx) }}
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(wd, "../../internal/db/migrations/001_init.sql"),
		filepath.Join(wd, "../internal/db/migrations/001_init.sql"),
		filepath.Join(wd, "internal/db/migrations/001_init.sql"),
	}
	var sqlBytes []byte
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil { sqlBytes = b; break }
	}
	if len(sqlBytes) == 0 { t.Fatalf("cannot find 001_init.sql from %s", wd) }
	if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil { t.Fatalf("migrate: %v", err) }
}