package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/Cypherspark/sms-gateway/internal/core"
)

type fakeProvider struct {
	mu    sync.Mutex
	sent  []struct{ To, Body string }
	failN int // first failN sends fail, then succeed
}

func (f *fakeProvider) Send(ctx context.Context, to, body string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failN > 0 {
		f.failN--
		return "", context.DeadlineExceeded
	}
	f.sent = append(f.sent, struct{ To, Body string }{to, body})
	return "prov-ok", nil
}

func newStore(t *testing.T) (*core.Store, func()) {
	db := startPostgres(t)
	return &core.Store{DB: db.Pool}, db.Term
}

func createUser(t *testing.T, s *core.Store, name string) string {
	id, err := s.CreateUser(context.Background(), name)
	require.NoError(t, err)
	return id
}

func topUp(t *testing.T, s *core.Store, user string, amount int) {
	require.NoError(t, s.TopUp(context.Background(), core.TopUpRequest{UserID: user, Amount: amount}))
}

func TestTopUpAndBalance(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 100)
	bal, err := s.GetBalance(context.Background(), uid)
	require.NoError(t, err)
	require.Equal(t, 100, bal)
}

func TestEnqueueAndCharge_IdempotentSingleDebit(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 10)

	key := "same-key"
	wg := sync.WaitGroup{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+491234", Body: "hi", IdempotencyKey: &key})
		}()
	}
	wg.Wait()

	bal, _ := s.GetBalance(context.Background(), uid)
	require.Equal(t, 9, bal)
}

func TestEnqueueInsufficientBalance(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	_, _, err := s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})
	require.Error(t, err)
}

func TestClaimSendAndMark_Success(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 2)
	_, _, err := s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "ok"})
	require.NoError(t, err)

	ids, err := s.ClaimQueuedMessages(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	_, to, body, err := s.LoadMessageForSend(context.Background(), ids[0])
	require.NoError(t, err)
	require.Equal(t, "+49", to)
	require.Equal(t, "ok", body)

	require.NoError(t, s.MarkSent(context.Background(), ids[0], "prov-1"))
}

func TestRefundOnPermanentFailure(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 1)
	msgID, _, err := s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})
	require.NoError(t, err)

	require.NoError(t, s.MarkFailedPermanentAndRefund(context.Background(), msgID))
	bal, _ := s.GetBalance(context.Background(), uid)
	require.Equal(t, 1, bal)
}

func TestConcurrentClaim_SkipLocked_NoDuplicates(t *testing.T) {
	s, term := newStore(t)
	defer term()
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 200)
	for i := 0; i < 100; i++ {
		_, _, _ = s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})
	}

	seen := make(map[string]bool)
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, _ := s.ClaimQueuedMessages(context.Background(), 25)
			mu.Lock(); defer mu.Unlock()
			for _, id := range ids {
				if seen[id] { t.Fatalf("duplicate claim: %s", id) }
				seen[id] = true
			}
		}()
	}
	wg.Wait()

	require.Len(t, seen, 100)
}
