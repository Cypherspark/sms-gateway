ALTER TABLE users
  ADD COLUMN last_served_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS users_last_served_at_idx ON users (last_served_at);