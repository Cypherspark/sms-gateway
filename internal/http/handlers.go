package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	dbgen "github.com/Cypherspark/sms-gateway/internal/db/gen"
	"github.com/Cypherspark/sms-gateway/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Server struct {
	Store *core.Store
}

func toPgTimestamptz(p *time.Time) pgtype.Timestamptz {
	if p == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *p, Valid: true}
}

func toNullMsgStatus(p *string) dbgen.NullMsgStatus {
	if p == nil {
		return dbgen.NullMsgStatus{Valid: false}
	}
	return dbgen.NullMsgStatus{
		MsgStatus: dbgen.MsgStatus(*p), // "queued" | "sending" | "sent" | "failed"
		Valid:     true,
	}
}

func NewServer(store *core.Store) *Server {
	return &Server{Store: store}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	r.Use(instrument)
	s.mountHealth(r)
	r.Post("/users", s.createUser)
	r.Post("/users/{id}/topup", s.topUp)
	r.Get("/users/{id}/balance", s.getBalance)
	r.Post("/messages", s.postMessage)
	r.Get("/messages", s.listMessages)
	r.Get("/messages/{id}", s.getMessage)
	s.mountDocs(r)
	s.mountMetrics(r)

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	id, err := s.Store.CreateUser(r.Context(), in.Name)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "name": in.Name})
}

func (s *Server) topUp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in struct {
		Amount int `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Amount <= 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid_amount"})
		return
	}
	if err := s.Store.TopUp(r.Context(), core.TopUpRequest{UserID: id, Amount: in.Amount}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getBalance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	bal, err := s.Store.GetBalance(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": id,
		"balance": bal,
	})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_X-User-ID"})
		return
	}

	idemp := r.Header.Get("Idempotency-Key")
	var key *string
	if idemp != "" {
		key = &idemp
	}

	var in struct {
		To   string `json:"to"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.To == "" || in.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}

	msgID, already, err := s.Store.EnqueueAndCharge(
		r.Context(),
		core.SendRequest{
			UserID:         userID,
			To:             in.To,
			Body:           in.Body,
			IdempotencyKey: key,
		},
	)
	if err != nil {
		if errors.Is(err, core.ErrInsufficientBalance) {
			metrics.APIEnqueue.WithLabelValues("insufficient_balance").Inc()
			writeJSON(w, http.StatusPaymentRequired, map[string]string{
				"error": "insufficient_balance",
			})
			return
		}
		metrics.APIEnqueue.WithLabelValues("error").Inc()
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	status := http.StatusAccepted
	if already {
		metrics.APIEnqueue.WithLabelValues("idempotent").Inc()
		status = http.StatusOK
	} else {
		metrics.APIEnqueue.WithLabelValues("ok").Inc()
	}

	writeJSON(w, status, map[string]any{
		"id":      msgID,
		"user_id": userID,
		"status":  "queued",
		"already": already,
	})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id_required"})
		return
	}

	// optional filters
	var statusPtr *string
	if v := r.URL.Query().Get("status"); v != "" {
		statusPtr = &v
	}

	var fromPtr, toPtr *time.Time
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			fromPtr = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			toPtr = &t
		}
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	params := dbgen.ListMessagesParams{
		UserID:  userID,
		Status:  toNullMsgStatus(statusPtr),
		FromTs:  toPgTimestamptz(fromPtr),
		ToTs:    toPgTimestamptz(toPtr),
		LimitN:  int32(limit),
		OffsetN: int32(offset),
	}

	items, err := s.Store.DB.Queries.ListMessages(r.Context(), params)

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id_required"})
		return
	}

	msg, err := s.Store.DB.Queries.GetMessage(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msg)
}
