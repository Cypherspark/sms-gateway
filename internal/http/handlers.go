package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/core"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	Store *core.Store
}

func NewServer(db *pgxpool.Pool) *Server { return &Server{Store: &core.Store{DB: db}} }

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)

	r.Post("/users", s.createUser)
	r.Post("/users/{id}/topup", s.topUp)
	r.Get("/users/{id}/balance", s.getBalance)
	r.Post("/messages", s.postMessage)
	r.Get("/messages", s.listMessages)
	r.Get("/messages/{id}", s.getMessage)
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
		writeJSON(w, 404, map[string]string{"error": "user_not_found"})
		return
	}
	writeJSON(w, 200, map[string]any{"balance": bal})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeJSON(w, 400, map[string]string{"error": "missing_X-User-ID"})
		return
	}
	idemp := r.Header.Get("Idempotency-Key")
	var key *string
	if idemp != "" {
		key = &idemp
	}
	var in struct{ To, Body string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.To == "" || in.Body == "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_body"})
		return
	}
	msgID, already, err := s.Store.EnqueueAndCharge(r.Context(), core.SendRequest{UserID: userID, To: in.To, Body: in.Body, IdempotencyKey: key})
	if err != nil {
		if errors.Is(err, errors.New("insufficient_balance")) {
			writeJSON(w, 402, map[string]string{"error": "insufficient_balance"})
			return
		}
		if err.Error() == "insufficient_balance" {
			writeJSON(w, 402, map[string]string{"error": "insufficient_balance"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusAccepted
	if already {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]string{"id": msgID})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeJSON(w, 400, map[string]string{"error": "user_id_required"})
		return
	}
	var status *string
	if v := r.URL.Query().Get("status"); v != "" {
		status = &v
	}
	var from, to *time.Time
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = &t
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
	items, err := s.Store.QueryMessages(r.Context(), userID, status, from, to, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	rows, err := s.Store.QueryMessages(r.Context(), "", nil, nil, nil, 1, 0)
	_ = rows
	_ = err // placeholder, implement if needed
	writeJSON(w, 501, map[string]string{"error": "not_implemented"})
}
