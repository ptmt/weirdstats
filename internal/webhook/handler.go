package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"weirdstats/internal/storage"
)

type Event struct {
	ObjectType     string                 `json:"object_type"`
	ObjectID       int64                  `json:"object_id"`
	AspectType     string                 `json:"aspect_type"`
	OwnerID        int64                  `json:"owner_id"`
	SubscriptionID int64                  `json:"subscription_id"`
	EventTime      int64                  `json:"event_time"`
	Updates        map[string]interface{} `json:"updates"`
}

type Handler struct {
	Store         *storage.Store
	VerifyToken   string
	SigningSecret string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		h.handleVerification(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if h.SigningSecret != "" {
		if !validSignature(payload, r.Header.Get("X-Strava-Signature"), h.SigningSecret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if event.ObjectType == "" || event.ObjectID == 0 || event.AspectType == "" || event.OwnerID == 0 {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	log.Printf("strava webhook: user=%d type=%s aspect=%s object=%d",
		event.OwnerID, event.ObjectType, event.AspectType, event.ObjectID)

	if err := h.recordEvent(ctx, event, string(payload)); err != nil {
		http.Error(w, "failed to record event", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) recordEvent(ctx context.Context, event Event, payload string) error {
	_, err := h.Store.InsertWebhookEvent(ctx, storage.WebhookEvent{
		ObjectID:   event.ObjectID,
		ObjectType: event.ObjectType,
		AspectType: event.AspectType,
		OwnerID:    event.OwnerID,
		RawPayload: payload,
	})
	if err != nil {
		return err
	}

	if event.ObjectType == "activity" && (event.AspectType == "create" || event.AspectType == "update") {
		if err := h.Store.EnqueueActivity(ctx, event.ObjectID); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) handleVerification(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("hub.challenge")
	verifyToken := r.URL.Query().Get("hub.verify_token")
	if challenge == "" {
		http.Error(w, "missing challenge", http.StatusBadRequest)
		return
	}
	if h.VerifyToken != "" && verifyToken != h.VerifyToken {
		http.Error(w, "invalid verify token", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"hub.challenge": challenge})
}

func validSignature(body []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	received, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, received)
}
