package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	dbpkg "github.com/Cypherspark/sms-gateway/internal/db"
	"github.com/Cypherspark/sms-gateway/internal/http"
	"github.com/Cypherspark/sms-gateway/internal/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var exitCode int
	defer func() {
		os.Exit(exitCode)
	}()

	dsn := env("DATABASE_URL", "postgres://sms:sms@localhost:5432/sms?sslmode=disable")

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(rootCtx, dsn)
	if err != nil {
		log.Printf("db: %v", err)
		exitCode = 1
		return
	}
	defer pool.Close()

	if err := pool.Ping(rootCtx); err != nil { // forces a real connection
		log.Printf("db ping: %v", err)
		exitCode = 1
		return
	}

	stopPoolMetrics := make(chan struct{})
	go metrics.NewPGXPoolStats(pool).Start(5*time.Second, stopPoolMetrics)
	defer close(stopPoolMetrics)

	database := dbpkg.NewDB(pool)
	coreStore := &core.Store{DB: database}

	srv := httpapi.NewServer(coreStore)
	host := env("HOST", "0.0.0.0")
	port := env("PORT", "8080")
	server := &http.Server{
		Addr:         host + ":" + port,
		Handler:      srv.Router(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("HTTP listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// ---- Graceful shutdown ----
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Printf("signal %v received, shutting down", s)
	case err := <-errCh:
		log.Printf("server: %v", err)
		exitCode = 1
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
		exitCode = 1
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
