package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Store struct{ DB *pgxpool.Pool }

const PricePerSMS = 1 // credits per single-part SMS

var (
	errInsufficientBalance = errors.New("insufficient_balance")
)

type SendRequest struct {
	UserID         string
	To             string
	Body           string
	IdempotencyKey *string
}

type TopUpRequest struct {
	UserID string
	Amount int
}

// CreateUser creates a user with initial balance 0 and returns id.
func (s *Store) CreateUser(ctx context.Context, name string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `INSERT INTO users(name) VALUES($1) RETURNING id`, name).Scan(&id)
	return id, err
}

func (s *Store) GetBalance(ctx context.Context, userID string) (int, error) {
	var bal int
	err := s.DB.QueryRow(ctx, `SELECT balance FROM users WHERE id=$1`, userID).Scan(&bal)
	return bal, err
}

func (s *Store) TopUp(ctx context.Context, req TopUpRequest) error {
	if req.Amount <= 0 {
		return fmt.Errorf("invalid amount")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE users SET balance = balance + $1 WHERE id=$2`, req.Amount, req.UserID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO balance_transactions(user_id, kind, amount) VALUES($1,'topup',$2)`, req.UserID, req.Amount)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) EnqueueAndCharge(ctx context.Context, r SendRequest) (msgID string, already bool, err error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Idempotency: return existing message if key seen.
	if r.IdempotencyKey != nil {
		var existsID string
		err = tx.QueryRow(ctx, `SELECT id FROM messages WHERE user_id=$1 AND idempotency_key=$2`, r.UserID, *r.IdempotencyKey).Scan(&existsID)
		if err == nil {
			return existsID, true, tx.Commit(ctx)
		}
		if err != pgx.ErrNoRows {
			return "", false, err
		}
	}

	// Lock user, check balance.
	var balance int
	err = tx.QueryRow(ctx, `SELECT balance FROM users WHERE id=$1 FOR UPDATE`, r.UserID).Scan(&balance)
	if err != nil {
		return "", false, err
	}
	if balance < PricePerSMS {
		return "", false, errInsufficientBalance
	}

	// Debit and enqueue message.
	_, err = tx.Exec(ctx, `UPDATE users SET balance = balance - $1 WHERE id=$2`, PricePerSMS, r.UserID)
	if err != nil {
		return "", false, err
	}

	if r.IdempotencyKey == nil {
		empty := ""
		r.IdempotencyKey = &empty
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO messages(user_id, to_msisdn, body, status, idempotency_key)
		VALUES($1,$2,$3,'queued',$4)
		RETURNING id
	`, r.UserID, r.To, r.Body, *r.IdempotencyKey).Scan(&msgID)
	if err != nil {
		return "", false, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO balance_transactions(user_id, kind, amount, message_id)
		VALUES($1,'debit',$2,$3)`, r.UserID, PricePerSMS, msgID)
	if err != nil {
		return "", false, err
	}

	return msgID, false, tx.Commit(ctx)
}

// ClaimQueuedMessages moves up to limit messages from queued->sending using SKIP LOCKED and returns their ids.
func (s *Store) ClaimQueuedMessages(ctx context.Context, limit int) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id FROM messages
		WHERE status='queued' AND send_after <= now()
		ORDER BY requested_at
		LIMIT $1 FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `UPDATE messages SET status='sending', attempts=attempts+1 WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}

func (s *Store) LoadMessageForSend(ctx context.Context, id string) (userID, to, body string, err error) {
	err = s.DB.QueryRow(ctx, `SELECT user_id, to_msisdn, body FROM messages WHERE id=$1`, id).Scan(&userID, &to, &body)
	return
}

func (s *Store) MarkSent(ctx context.Context, id, providerID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE messages SET status='sent', provider_message_id=$2, sent_at=now() WHERE id=$1`, id, providerID)
	return err
}

func (s *Store) MarkFailedWithRetry(ctx context.Context, id string, retryIn time.Duration) error {
	_, err := s.DB.Exec(ctx, `UPDATE messages SET status='queued', send_after=now()+$2::interval WHERE id=$1`, id, retryIn.String())
	return err
}

func (s *Store) MarkFailedPermanentAndRefund(ctx context.Context, id string) error {
	// Refund 1 credit to the user for this message if it never reached provider.
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM messages WHERE id=$1 FOR UPDATE`, id).Scan(&userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE messages SET status='failed' WHERE id=$1`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE users SET balance = balance + 1 WHERE id=$1`, userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO balance_transactions(user_id, kind, amount, message_id)
		VALUES($1,'refund',1,$2)`, userID, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// QueryMessages basic listing for reports.
func (s *Store) QueryMessages(ctx context.Context, userID string, status *string, from, to *time.Time, limit, offset int) ([]Message, error) {
	q := `SELECT id, user_id, to_msisdn, body, status, provider_message_id, error_code, requested_at, sent_at, delivered_at, attempts FROM messages WHERE user_id=$1`
	args := []any{userID}
	idx := 2
	if status != nil {
		q += fmt.Sprintf(" AND status=$%d", idx)
		args = append(args, *status)
		idx++
	}
	if from != nil {
		q += fmt.Sprintf(" AND requested_at >= $%d", idx)
		args = append(args, *from)
		idx++
	}
	if to != nil {
		q += fmt.Sprintf(" AND requested_at < $%d", idx)
		args = append(args, *to)
		idx++
	}
	q += fmt.Sprintf(" ORDER BY requested_at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var provID, errCode *string
		var sentAt, delAt *time.Time
		if err := rows.Scan(&m.ID, &m.UserID, &m.To, &m.Body, &m.Status, &provID, &errCode, &m.RequestedAt, &sentAt, &delAt, &m.Attempts); err != nil {
			return nil, err
		}
		m.ProviderMessageID = provID
		m.ErrorCode = errCode
		m.SentAt = sentAt
		m.DeliveredAt = delAt
		out = append(out, m)
	}
	return out, nil
}
