package core_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	database "github.com/Cypherspark/sms-gateway/internal/db"
	"github.com/stretchr/testify/require"
)


func newStore(t *testing.T) *core.Store {
	pg := database.StartTestPostgres(t)
	return &core.Store{DB: pg.Pool}
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
	s := newStore(t)
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 100)
	bal, err := s.GetBalance(context.Background(), uid)
	require.NoError(t, err)
	require.Equal(t, 100, bal)
}

func TestEnqueueAndCharge_IdempotentSingleDebit(t *testing.T) {
	s := newStore(t)
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
	s := newStore(t)
	uid := createUser(t, s, "acme")
	_, _, err := s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})
	require.Error(t, err)
}

func TestClaimSendAndMark_Success(t *testing.T) {
	s := newStore(t)
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
	s := newStore(t)
	uid := createUser(t, s, "acme")
	topUp(t, s, uid, 1)
	msgID, _, err := s.EnqueueAndCharge(context.Background(), core.SendRequest{UserID: uid, To: "+49", Body: "x"})
	require.NoError(t, err)

	require.NoError(t, s.MarkFailedPermanentAndRefund(context.Background(), msgID))
	bal, _ := s.GetBalance(context.Background(), uid)
	require.Equal(t, 1, bal)
}

func TestConcurrentClaim_SkipLocked_NoDuplicates(t *testing.T) {
    s := newStore(t)

    uid := createUser(t, s, "acme")
    topUp(t, s, uid, 200)

    const total = 100
    for i := 0; i < total; i++ {
		key := strconv.Itoa(i)
        _, _, err := s.EnqueueAndCharge(
            context.Background(),
            core.SendRequest{UserID: uid, To: "+49", Body: "x", IdempotencyKey: &key},
        )
        require.NoError(t, err)
    }

    // Sanity: ensure all enqueues are really queued
    var queued int
    err := s.DB.QueryRow(context.Background(),
        `SELECT COUNT(*) FROM messages WHERE status='queued'`).Scan(&queued)
    require.NoError(t, err)
    require.Equal(t, total, queued, "precondition failed: not all messages queued")

    seen := make(map[string]bool)
    var mu sync.Mutex
    var wg sync.WaitGroup

    workers := 8
    batch := 10
    var claimed int64

    // Hard stop so test can't hang
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // A small helper to record duplicates cleanly
    dup := func(id string) {
        t.Fatalf("duplicate claim: %s", id)
    }

    for w := 0; w < workers; w++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                // stop once target reached or context timed out
                if atomic.LoadInt64(&claimed) >= int64(total) {
                    return
                }
                select {
                case <-ctx.Done():
                    return
                default:
                }

                ids, err := s.ClaimQueuedMessages(context.Background(), batch)
                require.NoError(t, err)

                if len(ids) == 0 {
                    // May be empty momentarily while other workers are mid-commit.
                    // Back off briefly and keep trying until `claimed == total`.
                    time.Sleep(5 * time.Millisecond)
                    continue
                }

                mu.Lock()
                for _, id := range ids {
                    if seen[id] {
                        mu.Unlock()
                        dup(id)
                        return
                    }
                    seen[id] = true
                }
                mu.Unlock()

                atomic.AddInt64(&claimed, int64(len(ids)))
            }
        }()
    }

    wg.Wait()

    // If the timeout fired, this will be < total — fail with a helpful message.
    require.Equal(t, int64(total), atomic.LoadInt64(&claimed),
        "did not claim all messages before timeout")
    require.Len(t, seen, total)
}
