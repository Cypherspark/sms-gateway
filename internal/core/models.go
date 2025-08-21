package core

import (
	"time"
)

type Message struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	To                string    `json:"to"`
	Body              string    `json:"body"`
	Status            string    `json:"status"`
	ProviderMessageID *string   `json:"provider_message_id,omitempty"`
	ErrorCode         *string   `json:"error_code,omitempty"`
	RequestedAt       time.Time `json:"requested_at"`
	SentAt            *time.Time `json:"sent_at,omitempty"`
	DeliveredAt       *time.Time `json:"delivered_at,omitempty"`
	Attempts          int       `json:"attempts"`
}