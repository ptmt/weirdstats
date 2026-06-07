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

func TestSettings_ShowsFactPreferences(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:      202,
		AccessToken: "token",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}
	if err := store.ReplaceUserFactPreferences(ctx, 202, []storage.UserFactPreference{
		{FactID: weirdStatsFactStopSummary, Enabled: true, PostToStrava: true},
		{FactID: weirdStatsFactTrafficLightStops, Enabled: true, PostToStrava: true},
		{FactID: weirdStatsFactLongestSegment, Enabled: true, PostToStrava: true},
		{FactID: weirdStatsFactCoffeeStop, Enabled: false, PostToStrava: false},
		{FactID: weirdStatsFactRouteHighlights, Enabled: true, PostToStrava: false},
	}); err != nil {
		t.Fatalf("replace fact prefs: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activities/settings", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 202); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Settings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, text := range []string{
		"Weirdstats facts",
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
		"Auto-post every run",
		"Remarkable:",
	} {
		if !strings.Contains(body, text) {
			t.Fatalf("expected %q in settings page", text)
		}
	}
	if strings.Contains(body, `name="fact_coffee_stop" checked`) {
		t.Fatalf("expected coffee stop toggle to be disabled")
	}
	if !strings.Contains(body, `name="fact_route_highlights" checked`) {
		t.Fatalf("expected route highlights toggle to be enabled")
	}
	if strings.Contains(body, `name="fact_post_route_highlights" checked`) {
		t.Fatalf("expected route highlights auto-post toggle to be disabled")
	}
	if !strings.Contains(body, `name="fact_road_crossings" checked`) {
		t.Fatalf("expected road crossings toggle to default to enabled")
	}
	if !strings.Contains(body, `name="fact_post_road_crossings" checked`) {
		t.Fatalf("expected road crossings auto-post toggle to default to enabled")
	}
}

func TestSettings_UpdateFacts(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:      303,
		AccessToken: "token",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	body := strings.NewReader(strings.Join([]string{
		"action=update-facts",
		"fact_stop_summary=on",
		"fact_post_stop_summary=on",
		"fact_route_highlights=on",
	}, "&"))
	req := httptest.NewRequest(http.MethodPost, "/activities/settings", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 303); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Settings(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/activities/settings?msg=facts+updated" {
		t.Fatalf("unexpected redirect: %q", got)
	}

	settings, err := server.loadWeirdStatsFactSettings(ctx, 303)
	if err != nil {
		t.Fatalf("load fact settings: %v", err)
	}
	if !settings[weirdStatsFactStopSummary].Enabled {
		t.Fatalf("expected stop summary enabled")
	}
	if !settings[weirdStatsFactStopSummary].AutoPostEveryRun {
		t.Fatalf("expected stop summary auto-post enabled")
	}
	if settings[weirdStatsFactTrafficLightStops].Enabled {
		t.Fatalf("expected traffic-light stops disabled")
	}
	if settings[weirdStatsFactLongestSegment].Enabled {
		t.Fatalf("expected longest segment disabled")
	}
	if settings[weirdStatsFactCoffeeStop].Enabled {
		t.Fatalf("expected coffee stop disabled")
	}
	if !settings[weirdStatsFactRouteHighlights].Enabled {
		t.Fatalf("expected route highlights enabled")
	}
	if settings[weirdStatsFactRouteHighlights].AutoPostEveryRun {
		t.Fatalf("expected route highlights auto-post disabled")
	}
	if settings[weirdStatsFactRoadCrossings].Enabled {
		t.Fatalf("expected road crossings disabled")
	}
}

func TestSettings_ShowsPaceRuleTemplate(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:      404,
		AccessToken: "token",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activities/settings", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 404); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Settings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, text := range []string{
		"pace_sec_per_km",
		"Hide slow rides or runs",
		"6:00/km",
	} {
		if !strings.Contains(body, text) {
			t.Fatalf("expected %q in settings page", text)
		}
	}
}
