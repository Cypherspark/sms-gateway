package main

import (
	"context"
	"github.com/Cypherspark/sms-gateway/internal/core"
	dbpkg "github.com/Cypherspark/sms-gateway/internal/db"
	"github.com/Cypherspark/sms-gateway/internal/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	dsn := env("DATABASE_URL", "postgres://sms:sms@localhost:5432/sms?sslmode=disable")

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(rootCtx, dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(rootCtx); err != nil { // forces a real connection
		log.Fatalf("db ping: %v", err)
	}

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

	go func() {
		log.Printf("HTTP listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// ---- Graceful shutdown ----
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	cancel()
	_ = server.Shutdown(shutdownCtx)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
