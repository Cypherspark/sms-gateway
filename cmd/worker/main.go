package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cypherspark/sms-gateway/internal/core"     // your domain store using sqlc
	dbpkg "github.com/Cypherspark/sms-gateway/internal/db" // your sqlc wrapper: NewDB(*pgxpool.Pool)
	"github.com/Cypherspark/sms-gateway/internal/provider"
	wpkg "github.com/Cypherspark/sms-gateway/internal/worker"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var exitCode int
	defer func() {
		os.Exit(exitCode)
	}()

	dsn := env("DATABASE_URL", "postgres://sms:sms@localhost:5432/sms?sslmode=disable")

	opts := wpkg.WorkerOptions{
		BatchSize:     atoiEnv("WORKER_BATCH", 100),
		Concurrency:   atoiEnv("WORKER_CONCURRENCY", 16),
		PollInterval:  durEnv("WORKER_POLL_MS", 200*time.Millisecond),
		IdleSleep:     durEnv("WORKER_IDLE_MS", 300*time.Millisecond),
		DBBackoffMin:  durEnv("WORKER_DB_BACKOFF_MIN_MS", 200*time.Millisecond),
		DBBackoffMax:  durEnv("WORKER_DB_BACKOFF_MAX_MS", 5*time.Second),
		ProviderQPS:   atofEnv("PROVIDER_QPS", 500),    // tune per provider SLA
		ProviderBurst: atoiEnv("PROVIDER_BURST", 1000), // allow bursts
		SendTimeout:   durEnv("WORKER_SEND_TIMEOUT_MS", 5*time.Second),
	}

	// ---- Context / signals ----
	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ---- DB ----
	pool, err := pgxpool.New(rootCtx, dsn)
	if err != nil {
		log.Printf("db pool: %v", err)
		exitCode = 1
		return
	}
	defer pool.Close()

	if err := pool.Ping(rootCtx); err != nil {
		log.Printf("db ping: %v", err)
		exitCode = 1
		return
	}
	defer pool.Close()

	pg := dbpkg.NewDB(pool)
	store := &core.Store{DB: pg}

	// ---- Provider (wire your real impl here) ----
	prov := provider.NewDummy()

	// ---- Healthz ----
	go serveHealthz()

	// ---- Worker ----
	if err := wpkg.RunWorker(rootCtx, store, prov, opts); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("worker exited: %v", err)
		exitCode = 1
		return
	}
}

func serveHealthz() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	addr := env("HEALTH_ADDR", "0.0.0.0:9090")
	_ = http.ListenAndServe(addr, mux)
}

func atoiEnv(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func atofEnv(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
func durEnv(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Millisecond
		}
	}
	return def
}
