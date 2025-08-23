-- 001_init.sql — simple balance on users
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE msg_status AS ENUM ('queued','sending','sent','failed');

CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  balance    INTEGER NOT NULL CHECK (balance >=0) DEFAULT 0,          -- <- single source of truth
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT users_balance_nonnegative CHECK (balance >= 0)
);

CREATE TABLE messages (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  to_msisdn           TEXT NOT NULL,
  body                TEXT NOT NULL,
  status              msg_status NOT NULL DEFAULT 'queued',
  provider_message_id TEXT,
  error_code          TEXT,
  requested_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at             TIMESTAMPTZ,
  delivered_at        TIMESTAMPTZ,
  send_after          TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts            INTEGER NOT NULL DEFAULT 0,
  idempotency_key     TEXT,                        -- NULL when unused
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotency only when key IS NOT NULL
CREATE UNIQUE INDEX messages_user_id_idempotency_key_idx
  ON messages(user_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

-- Worker + reporting
CREATE INDEX messages_status_send_after_idx       ON messages(status, send_after);
CREATE INDEX messages_user_id_requested_at_idx    ON messages(user_id, requested_at DESC);

-- updated_at triggers
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END; $$ LANGUAGE plpgsql;

CREATE TRIGGER users_updated_at    BEFORE UPDATE ON users    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER messages_updated_at BEFORE UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION set_updated_at();
