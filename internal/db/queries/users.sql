-- name: CreateUser :one
INSERT INTO users (name)
VALUES ($1)
RETURNING id, name, balance, created_at, updated_at;

-- name: GetUser :one
SELECT id, name, balance, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetBalance :one
SELECT balance FROM users WHERE id = $1;

-- name: TopUp :exec
UPDATE users
SET balance = balance + $1
WHERE id = $2;

-- name: DebitIfEnough :execrows
UPDATE users
SET balance = balance - $1
WHERE id = $2 AND balance >= $1;

-- Optional: explicit row lock if you need it elsewhere
-- name: LockUser :exec
SELECT 1 FROM users WHERE id = $1 FOR UPDATE;
