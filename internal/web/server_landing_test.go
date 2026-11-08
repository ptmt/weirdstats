package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestLanding_ShowsDefaultEnabledFacts(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.Landing(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, text := range []string{
		"Optional stats for your Strava activities",
		"Turn any of them off in settings.",
		"Stop summary",
		"Longest segment",
		"Coffee stop",
		"Route highlights",
		"Road crossings",
		"Heart-rate change",
	} {
		if !strings.Contains(body, text) {
			t.Fatalf("expected %q in landing page", text)
		}
	}
	for _, text := range []string{"0 to 30 km/h", "0 to 40 km/h", "40 to 0 km/h", "30 to 0 km/h"} {
		if strings.Contains(body, text) {
			t.Fatalf("did not expect disabled-by-default fact %q in landing page", text)
		}
	}
	if strings.Contains(body, "Traffic-light stops") {
		t.Fatalf("did not expect separate traffic-light stops fact")
	}
	if !strings.Contains(body, `>Home</a>`) {
		t.Fatalf("expected Home link for signed-out visitors")
	}
}

func TestLanding_RedirectsSignedInUsersToActivities(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 202, AccessToken: "token"}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 202); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Landing(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/activities/" {
		t.Fatalf("unexpected redirect: %q", got)
	}

	messageReq := httptest.NewRequest(http.MethodGet, "/?msg=connection+failed", nil)
	for _, cookie := range sessionRec.Result().Cookies() {
		messageReq.AddCookie(cookie)
	}
	messageRec := httptest.NewRecorder()
	server.Landing(messageRec, messageReq)
	if got := messageRec.Header().Get("Location"); got != "/activities/?msg=connection+failed" {
		t.Fatalf("unexpected redirect with message: %q", got)
	}
}
