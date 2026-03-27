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

func TestLanding_ShowsOptionalFactList(t *testing.T) {
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
		"Traffic-light stops",
		"Longest segment",
		"Coffee stop",
		"Route highlights",
		"Road crossings",
		"0 to 30 km/h",
		"0 to 40 km/h",
		"40 to 0 km/h",
		"30 to 0 km/h",
	} {
		if !strings.Contains(body, text) {
			t.Fatalf("expected %q in landing page", text)
		}
	}
}
