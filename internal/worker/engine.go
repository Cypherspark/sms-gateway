package worker

import (
	"context"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	"github.com/Cypherspark/sms-gateway/internal/provider"
	"golang.org/x/time/rate"
)

type WorkerOptions struct {
	BatchSize     int           // how many to claim per poll
	Concurrency   int           // number of sender goroutines
	PollInterval  time.Duration // how often to poll when work is found
	IdleSleep     time.Duration // sleep when queue empty
	DBBackoffMin  time.Duration
	DBBackoffMax  time.Duration
	ProviderQPS   float64       // sustained provider rate
	ProviderBurst int           // burst to allow short spikes
	SendTimeout   time.Duration // per-send timeout
}

func RunWorker(ctx context.Context, store *core.Store, prov provider.Provider, opt WorkerOptions) error {
	// Rate limiter for provider (global for this worker process).
	limiter := rate.NewLimiter(rate.Limit(opt.ProviderQPS), opt.ProviderBurst)

	// Fixed-size worker pool.
	jobs := make(chan string, opt.BatchSize*2)
	var wg sync.WaitGroup
	wg.Add(opt.Concurrency)
	for i := 0; i < opt.Concurrency; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-jobs:
					sendOne(ctx, store, prov, limiter, id, opt.SendTimeout)
				}
			}
		}()
	}

	// Poll loop: claim batches and dispatch.
	dbBackoff := opt.DBBackoffMin
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		default:
		}

		ids, err := store.ClaimQueuedMessages(ctx, opt.BatchSize)
		if err != nil {
			// Backoff on DB errors (exponential + jitter)
			sleep := jitter(dbBackoff, 0.20)
			log.Printf("claim error: %v; backing off %s", err, sleep)
			time.Sleep(sleep)
			dbBackoff = minDur(opt.DBBackoffMax, time.Duration(float64(dbBackoff)*1.6))
			continue
		}
		dbBackoff = opt.DBBackoffMin // reset on success

		if len(ids) == 0 {
			time.Sleep(opt.IdleSleep)
			continue
		}

		// Dispatch to workers without unbounded goroutines
		for _, id := range ids {
			select {
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return ctx.Err()
			case jobs <- id:
			}
		}

		// short cadence while there is flow
		time.Sleep(opt.PollInterval)
	}
}

func sendOne(ctx context.Context, store *core.Store, prov provider.Provider, limiter *rate.Limiter, id string, sendTimeout time.Duration) {
	// Fetch data needed to send
	userID, to, body, err := store.LoadMessageForSend(ctx, id)
	if err != nil {
		_ = store.MarkFailedPermanent(ctx, id) // unable to load → fail hard
		return
	}

	// Respect provider rate limit (global in this process).
	if err := limiter.Wait(ctx); err != nil {
		// context canceled → exit handler
		return
	}

	// Per-send timeout
	cctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	providerID, err := prov.Send(cctx, to, body)
	if err != nil {
		// Basic retry policy (30s). You can make this exponential using attempts if you expose it.
		_ = store.MarkFailedWithRetry(ctx, id, 30*time.Second)
		return
	}

	_ = userID // reserved for future per-user throttling/bucketing
	_ = store.MarkSent(ctx, id, providerID)
}

func jitter(d time.Duration, frac float64) time.Duration {
	if frac <= 0 {
		return d
	}
	delta := int64(float64(d) * frac)
	if delta <= 0 {
		return d
	}
	// random in [-delta, +delta]
	n := rand.Int64N(2*delta+1) - delta
	return d + time.Duration(n)
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
