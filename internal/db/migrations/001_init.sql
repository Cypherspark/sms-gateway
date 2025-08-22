CREATE EXTENSION IF NOT EXISTS pgcrypto; -- for gen_random_uuid()

CREATE TYPE msg_status AS ENUM ('queued','sending','sent','failed');

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    balance INTEGER NOT NULL CHECK (balance >= 0) DEFAULT 0, -- credits
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE balance_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL CHECK (kind IN ('topup','debit','refund')),
    amount INTEGER NOT NULL CHECK (amount > 0),
    message_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON balance_transactions(user_id, created_at);

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    to_msisdn TEXT NOT NULL,
    body TEXT NOT NULL,
    status msg_status NOT NULL DEFAULT 'queued',
    provider_message_id TEXT,
    error_code TEXT,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    send_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ON messages(user_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX ON messages(user_id, requested_at);
CREATE INDEX ON messages(status, send_after);

-- Trigger to auto-update updated_at
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_updated_at BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER messages_updated_at BEFORE UPDATE ON messages
FOR EACH ROW EXECUTE FUNCTION set_updated_at();