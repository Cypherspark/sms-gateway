package db

import (
	"context"
	"embed"
	"fmt"
	"github.com/docker/go-connections/nat"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func retry(n int, fn func() error) error {
	backoff := 200 * time.Millisecond
	for i := 0; i < n; i++ {
		if err := fn(); err == nil {
			return nil
		}
		time.Sleep(backoff)
		if backoff < 3*time.Second {
			backoff *= 2
		}
	}
	return fmt.Errorf("retry: giving up after %d tries", n)
}

func StartTestPostgres(t testing.TB) *DB {
	t.Helper()

	// generous timeout for CI
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	// IMPORTANT: use non-alpine + SQL wait (auth-ready, not just TCP open)
	req := tc.ContainerRequest{
		Image: "postgres:16-alpine",
		Env: map[string]string{
			"POSTGRES_USER":     "sms",
			"POSTGRES_PASSWORD": "sms",
			"POSTGRES_DB":       "sms",
		},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor: wait.ForSQL("5432/tcp", "pgx", func(host string, port nat.Port) string {
			// DSN used only for readiness probing
			return fmt.Sprintf("host=%s port=%s user=sms password=sms dbname=sms sslmode=disable", host, port.Port())
		}).WithStartupTimeout(120 * time.Second).WithPollInterval(300 * time.Millisecond),
	}

	c, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}

	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mp, err := c.MappedPort(ctx, "5432/tcp") // NOTE: must include /tcp
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://sms:sms@%s:%s/sms?sslmode=disable", host, mp.Port())

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// make pool slightly more forgiving in CI
	cfg.MaxConns = 4
	cfg.MinConns = 0

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Ensure the server fully accepts queries
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pg ping: %v", err)
	}

	// Retry migrations briefly — first heavy DDL can still race autovacuum/startup bits
	if err := retry(6, func() error {
		return applyMigrationsOnce(ctx, pool)
	}); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return NewDB(pool)
}

func applyMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // 001_, 002_, ...

	for _, name := range files {
		path := filepath.ToSlash("migrations/" + name)
		sqlBytes, err := migrationsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}
