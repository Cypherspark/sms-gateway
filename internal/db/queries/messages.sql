-- name: InsertMessage :one
INSERT INTO messages (user_id, to_msisdn, body, status, idempotency_key)
VALUES (
  sqlc.arg(user_id),
  sqlc.arg(to_msisdn),
  sqlc.arg(body),
  'queued',
  sqlc.narg(idempotency_key)     
)
RETURNING id;

-- name: GetMessageByIdemKey :one
SELECT id
FROM messages
WHERE user_id = $1 AND idempotency_key = $2;

-- name: ClaimQueued :many
WITH picked AS (
  SELECT id
  FROM messages
  WHERE status = 'queued' AND send_after <= now()
  ORDER BY requested_at
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE messages m
SET status = 'sending', attempts = attempts + 1
FROM picked
WHERE m.id = picked.id
RETURNING m.id;

-- name: LoadMessageForSend :one
SELECT user_id, to_msisdn, body
FROM messages
WHERE id = $1;

-- name: MarkSent :exec
UPDATE messages
SET status = 'sent', provider_message_id = $2, sent_at = now()
WHERE id = $1;

-- name: RequeueWithBackoff :exec
UPDATE messages
SET status = 'queued',
    send_after = now() + (sqlc.arg(seconds) || ' seconds')::interval
WHERE id = sqlc.arg(id);

-- name: MarkFailed :exec
UPDATE messages
SET status='failed'
WHERE id = $1;

-- name: ListMessages :many
SELECT id, user_id, to_msisdn, body, status, provider_message_id, error_code,
       requested_at, sent_at, delivered_at, attempts
FROM messages
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(status)::msg_status     IS NULL OR status       = sqlc.narg(status)::msg_status)
  AND (sqlc.narg(from_ts)::timestamptz   IS NULL OR requested_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz     IS NULL OR requested_at <  sqlc.narg(to_ts)::timestamptz)
ORDER BY requested_at DESC
LIMIT  sqlc.arg(limit_n)
OFFSET sqlc.arg(offset_n);


-- name: MarkFailedAndRefund :exec
WITH upd AS (
  UPDATE messages AS m
  SET status = 'failed'
  WHERE m.id = $1
    AND status <> 'failed'
  RETURNING user_id
)
UPDATE users AS u
SET balance = balance + 1
WHERE u.id = (SELECT user_id FROM upd);

-- name: GetMessage :one
SELECT id, user_id, to_msisdn, body, status, provider_message_id, error_code,
       requested_at, sent_at, delivered_at, attempts
FROM messages
WHERE id = sqlc.arg(id);

-- name: ClaimQueuedLRS :many
WITH next_users AS (
  SELECT u.id
  FROM users u
  WHERE EXISTS (
    SELECT 1
    FROM messages m
    WHERE m.user_id = u.id
      AND m.status = 'queued'
      AND m.send_after <= now()
  )
  ORDER BY u.last_served_at NULLS FIRST
  LIMIT sqlc.arg(user_slots_n)
),
cand AS (
  SELECT x.id, x.user_id
  FROM next_users u
  CROSS JOIN LATERAL (
    SELECT m.id, m.user_id, m.requested_at
    FROM messages m
    WHERE m.user_id = u.id
      AND m.status = 'queued'
      AND m.send_after <= now()
    ORDER BY m.requested_at
    LIMIT sqlc.arg(per_user_n)
    FOR UPDATE SKIP LOCKED
  ) AS x
  ORDER BY x.requested_at
  LIMIT sqlc.arg(limit_n)
),
upd AS (
  UPDATE messages m
  SET status = 'sending', updated_at = now()
  WHERE m.id IN (SELECT id FROM cand)
    AND m.status = 'queued'
  RETURNING m.id, m.user_id
),
bump AS (
  UPDATE users u
  SET last_served_at = now()
  WHERE u.id IN (SELECT DISTINCT user_id FROM upd)
  RETURNING u.id
)
SELECT id FROM upd;
