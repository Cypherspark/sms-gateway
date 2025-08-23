package worker

import (
	"context"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	"github.com/Cypherspark/sms-gateway/internal/metrics"
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
	limiter := rate.NewLimiter(rate.Limit(opt.ProviderQPS), opt.ProviderBurst)

	// Fixed-size worker pool reading from a jobs channel.
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
				case id, ok := <-jobs:
					if !ok {
						return
					}
					sendOne(ctx, store, prov, limiter, id, opt.SendTimeout)
				}
			}
		}()
	}

	// Poll loop: claim and dispatch.
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
			metrics.ClaimTotal.WithLabelValues("error").Inc()
			sleep := jitter(dbBackoff, 0.20)
			log.Printf("claim error: %v; backing off %s", err, sleep)
			time.Sleep(sleep)
			dbBackoff = minDur(opt.DBBackoffMax, time.Duration(float64(dbBackoff)*1.6))
			continue
		}
		dbBackoff = opt.DBBackoffMin // reset on success

		if len(ids) == 0 {
			metrics.ClaimTotal.WithLabelValues("empty").Inc()
			time.Sleep(opt.IdleSleep)
			continue
		}

		metrics.ClaimTotal.WithLabelValues("ok").Add(float64(len(ids)))
		metrics.ClaimBatchSize.Observe(float64(len(ids)))

		// Dispatch to workers without unbounded goroutines.
		for _, id := range ids {
			select {
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return ctx.Err()
			case jobs <- id:
			}
		}

		time.Sleep(opt.PollInterval) // short cadence while there is flow
	}
}

func sendOne(ctx context.Context, store *core.Store, prov provider.Provider, limiter *rate.Limiter, id string, sendTimeout time.Duration) {
	userID, to, body, err := store.LoadMessageForSend(ctx, id)
	if err != nil {
		_ = store.MarkFailedPermanent(ctx, id)
		return
	}

	// Respect provider rate limit (global in this process).
	if err := limiter.Wait(ctx); err != nil {
		return
	}

	metrics.InFlight.Inc()
	defer metrics.InFlight.Dec()

	cctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	start := time.Now()
	providerID, err := prov.Send(cctx, to, body)
	metrics.ProviderSendDuration.Observe(time.Since(start).Seconds())

	if err != nil {
		_ = store.MarkFailedWithRetry(ctx, id, 30*time.Second)
		metrics.ProviderSendTotal.WithLabelValues("temp_fail").Inc()
		metrics.RetryTotal.Inc()
		return
	}

	_ = userID // reserved for future per-user throttles/buckets
	_ = store.MarkSent(ctx, id, providerID)
	metrics.ProviderSendTotal.WithLabelValues("sent").Inc()
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
