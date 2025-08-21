## Notes

- Atomic balance check: `SELECT ... FOR UPDATE` on `users` + debit + message insert in a single tx.
- Idempotency: unique `(user_id, idempotency_key)` prevents double charge.
- Worker: claims via `FOR UPDATE SKIP LOCKED` -> converts to `sending` -> sends -> marks `sent` or re-queues with backoff; refunds on permanent pre-submit failure.
- Scale: run multiple API replicas (stateless) and multiple workers — SKIP LOCKED prevents double processing. Partition messages by `user_id` if needed later.
- Observability: add Prometheus counters (accepted, sent, failed), latency histograms, per-user rate limits if required.

      