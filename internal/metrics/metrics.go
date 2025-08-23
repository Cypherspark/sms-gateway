package metrics

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// API
	HTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "http_requests_total", Help: "Count of HTTP requests."},
		[]string{"handler", "method", "code"},
	)
	HTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12), // 5ms..~10s
		},
		[]string{"handler", "method"},
	)
	APIEnqueue = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "api_enqueue_total", Help: "Enqueue results."},
		[]string{"result"}, // ok | idempotent | insufficient_balance | error
	)

	// Worker
	ClaimTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "worker_claim_total", Help: "Claim attempts."},
		[]string{"result"}, // ok | empty | error
	)
	ClaimBatchSize = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "worker_claim_batch_size",
			Help:    "Number of IDs returned per claim.",
			Buckets: prometheus.LinearBuckets(0, 10, 11), // 0,10,...,100
		},
	)
	InFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{Name: "worker_inflight", Help: "In-flight messages in this process."},
	)
	ProviderSendTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "provider_send_total", Help: "Provider send outcomes."},
		[]string{"outcome"}, // sent | temp_fail | perm_fail
	)
	ProviderSendDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "provider_send_duration_seconds",
			Help:    "Provider send latency.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms..~40s
		},
	)
	RetryTotal  = prometheus.NewCounter(prometheus.CounterOpts{Name: "worker_retry_total", Help: "Retries scheduled."})
	RefundTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "worker_refund_total", Help: "Refunds after perm fail."})
)

// Register default + our collectors
func MustRegister() {
	prometheus.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		HTTPRequests, HTTPDuration, APIEnqueue,
		ClaimTotal, ClaimBatchSize, InFlight,
		ProviderSendTotal, ProviderSendDuration, RetryTotal, RefundTotal,
	)
}

// Export a tiny pgxpool stats exporter
type PGXPoolStats struct {
	pool *pgxpool.Pool

	conns          prometheus.Gauge
	idle           prometheus.Gauge
	acquireCount   prometheus.Counter
	acquireLatency prometheus.Counter
}

func NewPGXPoolStats(pool *pgxpool.Pool) *PGXPoolStats {
	m := &PGXPoolStats{
		pool: pool,
		conns: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_conns", Help: "Total connections in pool.",
		}),
		idle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_idle_conns", Help: "Idle connections in pool.",
		}),
		acquireCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_pool_acquires_total", Help: "Total pool acquires.",
		}),
		acquireLatency: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_pool_acquire_seconds_total", Help: "Sum of acquire latencies.",
		}),
	}
	prometheus.MustRegister(m.conns, m.idle, m.acquireCount, m.acquireLatency)

	return m
}

func (m *PGXPoolStats) Start(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	for {
		select {
		case <-stop:
			t.Stop()
			return
		case <-t.C:
			s := m.pool.Stat()
			m.conns.Set(float64(s.TotalConns()))
			m.idle.Set(float64(s.IdleConns()))
			m.acquireCount.Add(float64(s.AcquireCount()))
			m.acquireLatency.Add(s.AcquireDuration().Seconds())
		}
	}
}
