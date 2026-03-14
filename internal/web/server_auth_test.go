package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestActivities_RedirectsAnonymousUsersToSignIn(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/activities/", nil)
	rec := httptest.NewRecorder()

	server.Activities(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/connect/strava?next=%2Factivities%2F" {
		t.Fatalf("unexpected redirect: %q", got)
	}
}

func TestActivities_ShowsOnlyCurrentUserActivities(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	for _, token := range []storage.StravaToken{
		{UserID: 101, AccessToken: "token-a", AthleteID: 101, AthleteName: "Alice Example"},
		{UserID: 202, AccessToken: "token-b", AthleteID: 202, AthleteName: "Bob Example"},
	} {
		if err := store.UpsertStravaToken(ctx, token); err != nil {
			t.Fatalf("upsert token %d: %v", token.UserID, err)
		}
	}

	start := time.Date(2026, time.March, 15, 8, 0, 0, 0, time.UTC)
	for _, activity := range []storage.Activity{
		{UserID: 101, Type: "Ride", Name: "Alice Secret Ride", StartTime: start},
		{UserID: 202, Type: "Ride", Name: "Bob Visible Ride", StartTime: start.Add(time.Hour)},
	} {
		if _, err := store.InsertActivity(ctx, activity, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: activity.StartTime, Speed: 6}}); err != nil {
			t.Fatalf("insert activity %q: %v", activity.Name, err)
		}
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activities/", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 202); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Activities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Alice Secret Ride") {
		t.Fatalf("expected Alice activity to be hidden from Bob session")
	}
	if !strings.Contains(body, "Bob Visible Ride") {
		t.Fatalf("expected Bob activity in response")
	}
}
