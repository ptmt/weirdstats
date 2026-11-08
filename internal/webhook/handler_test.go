package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 7, AccessToken: "test"}); err != nil {
		t.Fatal(err)
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

func TestDeleteAndDeauthorizationPurgeAndIgnoreDelayedEvents(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 7, AccessToken: "fake"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpsertActivity(ctx, storage.Activity{ID: 42, UserID: 7, Name: "Ride", Type: "Ride", StartTime: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.EnqueueActivity(ctx, 42, 7); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: store}
	if err = h.recordEvent(ctx, Event{ObjectID: 42, ObjectType: "activity", OwnerID: 7, AspectType: "delete"}, "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetActivity(ctx, 42); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("activity retained: %v", err)
	}
	if rows, err := store.ListJobs(ctx, 10); err != nil || len(rows) != 0 {
		t.Fatalf("jobs retained: %v %v", rows, err)
	}
	if err = h.recordEvent(ctx, Event{ObjectID: 7, ObjectType: "athlete", OwnerID: 7, AspectType: "update", Updates: map[string]interface{}{"authorized": "false"}}, "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetStravaToken(ctx, 7); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("token retained: %v", err)
	}
	if err = h.recordEvent(ctx, Event{ObjectID: 42, ObjectType: "activity", OwnerID: 7, AspectType: "create"}, "sensitive event"); err != nil {
		t.Fatal(err)
	}
	if count, err := store.CountWebhookEvents(ctx); err != nil || count != 0 {
		t.Fatalf("event recreated data: %d %v", count, err)
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
