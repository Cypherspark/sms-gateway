package provider

import (
	"context"
)

type Provider interface {
	Send(ctx context.Context, to, body string) (providerMsgID string, err error)
}
