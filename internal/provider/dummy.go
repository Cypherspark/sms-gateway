package provider

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

type Dummy struct{}

func NewDummy() *Dummy { return &Dummy{} }

func (d *Dummy) Send(ctx context.Context, to, body string) (string, error) {
	// Simulate latency and occasional failures.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}
	if rand.Intn(100) < 3 { // ~3% failure
		return "", errors.New("provider_temporary_error")
	}
	return "prov-" + randomID(), nil
}

func randomID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b { b[i] = letters[r.Intn(len(letters))] }
	return string(b)
}