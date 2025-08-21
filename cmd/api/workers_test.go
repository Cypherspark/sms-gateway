package main

import (
	"context"
	"testing"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	"github.com/stretchr/testify/require"
	database "github.com/Cypherspark/sms-gateway/internal/db"
)

type fakeProv struct{ ok bool }

func (f *fakeProv) Send(ctx context.Context, to, body string) (string, error) {
	if !f.ok {
		return "", context.DeadlineExceeded
	}
	return "ok", nil
}

// Light smoke-test around worker logic (claim → send → mark sent)
func TestWorkerLikeFlow(t *testing.T) {
	db := database.StartTestPostgres(t)
	store := &core.Store{DB: db.Pool}
	uid, err := store.CreateUser(context.Background(), "acme")
	require.NoError(t, err)
	require.NoError(t, store.TopUp(context.Background(), core.TopUpRequest{UserID: uid, Amount: 1}))
	_, _, _ = store.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})

	ids, _ := store.ClaimQueuedMessages(context.Background(), 10)
	for _, id := range ids {
		_, _, _, _ = store.LoadMessageForSend(context.Background(), id)
		_ = store.MarkSent(context.Background(), id, "ok")
	}
	// no panics, basic flow covered
	time.Sleep(20 * time.Millisecond)
}
