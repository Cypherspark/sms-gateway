package core

import (
	"context"
	"errors"
	"strconv"
	"time"

	db "github.com/Cypherspark/sms-gateway/internal/db"
	dbgen "github.com/Cypherspark/sms-gateway/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	DB *db.DB
}

const PricePerSMS = 1

var errInsufficientBalance = errors.New("insufficient_balance")

func toPgText(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// ---- Users / Balance ----

func (s *Store) CreateUser(ctx context.Context, name string) (string, error) {
	u, err := s.DB.Queries.CreateUser(ctx, name)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

func (s *Store) GetBalance(ctx context.Context, userID string) (int, error) {
	bal, err := s.DB.Queries.GetBalance(ctx, userID)
	return int(bal), err
}

type TopUpRequest struct {
	UserID string
	Amount int
}

func (s *Store) TopUp(ctx context.Context, req TopUpRequest) error {
	if req.Amount <= 0 {
		return errors.New("invalid amount")
	}
	return s.DB.Queries.TopUp(ctx, dbgen.TopUpParams{
		Balance: int32(req.Amount),
		ID:      req.UserID,
	})
}

// ---- Messages / Send flow ----

type SendRequest struct {
	UserID         string
	To             string
	Body           string
	IdempotencyKey *string
}

// Debit + enqueue atomically; idempotent when key is provided.
func (s *Store) EnqueueAndCharge(ctx context.Context, r SendRequest) (msgID string, already bool, err error) {
	err = s.DB.WithTx(ctx, func(q *dbgen.Queries) error {
		// 1) Idempotency check (only if provided)
		if r.IdempotencyKey != nil {
			id, e := q.GetMessageByIdemKey(ctx, dbgen.GetMessageByIdemKeyParams{
				UserID:         r.UserID,
				IdempotencyKey: toPgText(r.IdempotencyKey),
			})
			if e == nil {
				msgID = id
				already = true
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}

		// 2) Conditional debit (locks row; returns 0 rows if insufficient)
		rows, e := q.DebitIfEnough(ctx, dbgen.DebitIfEnoughParams{
			Balance: int32(PricePerSMS),
			ID:      r.UserID,
		})
		if e != nil {
			return e
		}
		if rows == 0 {
			return errInsufficientBalance
		}

		// 3) Insert message (idempotency_key may be NULL)
		id, e := q.InsertMessage(ctx, dbgen.InsertMessageParams{
			UserID:         r.UserID,
			ToMsisdn:       r.To,
			Body:           r.Body,
			IdempotencyKey: toPgText(r.IdempotencyKey),
		})
		if e != nil {
			return e
		}
		msgID = id
		return nil
	})
	return msgID, already, err
}

// Worker helpers

func (s *Store) ClaimQueuedMessages(ctx context.Context, limit int) ([]string, error) {
	ids, err := s.DB.Queries.ClaimQueued(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

func (s *Store) LoadMessageForSend(ctx context.Context, id string) (userID, to, body string, err error) {
	row, err := s.DB.Queries.LoadMessageForSend(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	return row.UserID, row.ToMsisdn, row.Body, nil
}

func (s *Store) MarkSent(ctx context.Context, id, providerID string) error {
	return s.DB.Queries.MarkSent(ctx, dbgen.MarkSentParams{
		ID:                id,
		ProviderMessageID: toPgText(&providerID),
	})
}

func (s *Store) MarkFailedWithRetry(ctx context.Context, id string, retryIn time.Duration) error {
	sec := strconv.Itoa(int((retryIn / time.Second)))
	return s.DB.Queries.RequeueWithBackoff(ctx, dbgen.RequeueWithBackoffParams{
		ID:      id,
		Seconds: toPgText(&sec),
	})
}

func (s *Store) MarkFailedPermanent(ctx context.Context, id string) error {
	return s.DB.Queries.MarkFailed(ctx, id)
}

func (s *Store) MarkFailedPermanentAndRefund(ctx context.Context, id string) error {
	return s.DB.Queries.MarkFailedAndRefund(ctx, id)
}
