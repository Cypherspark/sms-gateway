package db

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func StartTestPostgres(t testing.TB) *DB {
	t.Helper()
	ctx := context.Background()

	req := tc.ContainerRequest{
		Image:        "postgres:16-alpine",
		Env:          map[string]string{"POSTGRES_USER": "sms", "POSTGRES_PASSWORD": "sms", "POSTGRES_DB": "sms"},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://sms:sms@%s:%s/sms?sslmode=disable", host, port.Port())

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("pgxpool: %v", err)
	}

	applyMigrations(t, ctx, pool)

	db := NewDB(pool)
	t.Cleanup(func() {
		pool.Close()
		_ = c.Terminate(context.Background())
	})
	return db
}

func applyMigrations(t testing.TB, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	// Load *.sql files from the embedded FS, sort by filename, exec in order.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
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
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}
