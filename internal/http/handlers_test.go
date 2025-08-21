package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/Cypherspark/sms-gateway/internal/http"
)

func startAPI(t *testing.T) (*httpapi.Server, func()) {
	pg := startPostgres(t)
	srv := httpapi.NewServer(pg.Pool)
	return srv, func() { pg.Term() }
}

func TestCreateUserTopUpSend_ListAndBalance(t *testing.T) {
	srv, term := startAPI(t)
	defer term()
	h := srv.Router()

	// 1) create user
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users", bytes.NewBufferString(`{"name":"acme"}`))
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var user map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &user)
	uid := user["id"].(string)

	// 2) top up
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/users/"+uid+"/topup", bytes.NewBufferString(`{"amount":5}`))
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// 3) send (idempotent)
	body := bytes.NewBufferString(`{"to":"+49","body":"hello"}`)
	req = httptest.NewRequest("POST", "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", uid)
	req.Header.Set("Idempotency-Key", "k1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	var msgResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &msgResp)
	firstID := msgResp["id"]

	// Repeat same request → must be 200 with same id
	body = bytes.NewBufferString(`{"to":"+49","body":"hello"}`)
	req = httptest.NewRequest("POST", "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", uid)
	req.Header.Set("Idempotency-Key", "k1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	_ = json.Unmarshal(w.Body.Bytes(), &msgResp)
	require.Equal(t, firstID, msgResp["id"])

	// 4) list messages
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/messages?user_id="+uid+"&limit=10", nil)
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// 5) balance endpoint
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/users/"+uid+"/balance", nil)
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}