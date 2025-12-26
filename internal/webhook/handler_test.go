package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"weirdstats/internal/storage"
)

func TestHandlerStoresEventAndEnqueues(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	handler := &Handler{Store: store, SigningSecret: "secret"}
	payload := []byte(`{"object_type":"activity","object_id":42,"aspect_type":"create","owner_id":7}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Strava-Signature", signPayload(payload, "secret"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	count, err := store.CountWebhookEvents(ctx)
	if err != nil {
		t.Fatalf("count webhook events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 webhook event, got %d", count)
	}

	queueCount, err := store.CountQueue(ctx)
	if err != nil {
		t.Fatalf("count queue: %v", err)
	}
	if queueCount != 1 {
		t.Fatalf("expected 1 queued activity, got %d", queueCount)
	}
}

func TestHandlerRejectsMissingFields(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	handler := &Handler{Store: store, SigningSecret: "secret"}
	payload := []byte(`{"object_type":"activity"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Strava-Signature", signPayload(payload, "secret"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerVerification(t *testing.T) {
	handler := &Handler{VerifyToken: "verify-token"}
	req := httptest.NewRequest(http.MethodGet, "/webhook?hub.challenge=abc&hub.verify_token=verify-token", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "{\"hub.challenge\":\"abc\"}\n" {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
