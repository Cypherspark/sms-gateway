package main

import (
	"context"
	"log"
	"os"
	"net/http"
	"os/signal"
	"syscall"
	"time"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Cypherspark/sms-gateway/internal/core"
	"github.com/Cypherspark/sms-gateway/internal/http"
	"github.com/Cypherspark/sms-gateway/internal/provider"
)

func main() {
	dsn := env("DATABASE_URL", "postgres://sms:sms@localhost:5432/sms?sslmode=disable")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil { log.Fatalf("db: %v", err) }
	defer pool.Close()

	// Start worker
	store := &core.Store{DB: pool}
	prov := provider.NewDummy()
	go startWorker(ctx, store, prov)

	// HTTP server
	srv := httpapi.NewServer(pool)
	host := env("HOST", "0.0.0.0"); port := env("PORT", "8080")
	server := &http.Server{ Addr: host+":"+port, Handler: srv.Router(), ReadTimeout: 5*time.Second, WriteTimeout: 10*time.Second }
	go func(){
		log.Printf("HTTP listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Fatalf("server: %v", err) }
	}()

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func startWorker(ctx context.Context, store *core.Store, prov provider.Provider) {
	// Simple loop; in production consider multiple goroutines and backoff.
	for {
		select { case <-ctx.Done(): return; default: }
		ids, err := store.ClaimQueuedMessages(ctx, 100)
		if err != nil { time.Sleep(200*time.Millisecond); continue }
		if len(ids) == 0 { time.Sleep(150 * time.Millisecond); continue }
		for _, id := range ids {
			go func(id string) {
				// Claim was done; now send.
				userID, to, body, err := store.LoadMessageForSend(ctx, id)
				if err != nil { _ = store.MarkFailedPermanentAndRefund(ctx, id); return }
				_ = userID // not used, but here for potential per-user throttles
				providerID, err := prov.Send(ctx, to, body)
				if err != nil {
					// Retry after a short delay; escalate with attempts if needed.
					_ = store.MarkFailedWithRetry(ctx, id, 30*time.Second)
					return
				}
				_ = store.MarkSent(ctx, id, providerID)
			}(id)
		}
	}
}

func env(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }
