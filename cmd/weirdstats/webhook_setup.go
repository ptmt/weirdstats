package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"weirdstats/internal/config"
	"weirdstats/internal/strava"
)

func ensureWebhookSubscription(ctx context.Context, cfg config.Config) {
	if !cfg.StravaWebhookAutoRegister {
		return
	}
	if cfg.StravaWebhookCallbackURL == "" {
		log.Printf("webhook auto-register skipped: BASE_URL not set")
		return
	}
	if cfg.StravaVerifyToken == "" {
		log.Printf("webhook auto-register skipped: STRAVA_VERIFY_TOKEN not set")
		return
	}
	if cfg.StravaClientID == "" || cfg.StravaClientSecret == "" {
		log.Printf("webhook auto-register skipped: Strava client credentials missing")
		return
	}

	client := &strava.WebhookClient{
		BaseURL:      cfg.StravaBaseURL,
		ClientID:     cfg.StravaClientID,
		ClientSecret: cfg.StravaClientSecret,
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	action, subscription, err := client.EnsureSubscription(
		timeoutCtx,
		cfg.StravaWebhookCallbackURL,
		cfg.StravaVerifyToken,
		cfg.StravaWebhookAutoReplace,
	)
	if err != nil {
		if errors.Is(err, strava.ErrSubscriptionMismatch) {
			log.Printf("webhook subscription mismatch: existing callback differs from %q (set STRAVA_WEBHOOK_AUTO_REPLACE=true to replace)",
				cfg.StravaWebhookCallbackURL)
			return
		}
		log.Printf("webhook auto-register failed: %v", err)
		return
	}

	log.Printf("webhook subscription %s (id=%d callback=%s)", action, subscription.ID, subscription.CallbackURL)
}
