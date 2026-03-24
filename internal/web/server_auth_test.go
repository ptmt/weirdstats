package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
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

func TestActivities_ShowsStravaDescriptionAndDetectedFactCount(t *testing.T) {
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
		AthleteID:   404,
		AthleteName: "Dora Example",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 16, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      404,
		Type:        "Ride",
		Name:        "Lunch Loop",
		StartTime:   start,
		Description: "Met up with Sam at the cafe.\n\n2 stops (42s total) · 1 at lights #weirdstats",
	}, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: start, Speed: 6}})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.UpsertActivityStats(ctx, activityID, stats.StopStats{
		StopCount:             2,
		StopTotalSeconds:      42,
		TrafficLightStopCount: 1,
		RoadCrossingCount:     2,
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activities/", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 404); err != nil {
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
	if !strings.Contains(body, "Met up with Sam at the cafe.") {
		t.Fatalf("expected plain Strava description in response")
	}
	if !strings.Contains(body, "2 detected facts") {
		t.Fatalf("expected detected fact count in response")
	}
	if !strings.Contains(body, "2 crossings") {
		t.Fatalf("expected official road crossing stat in response")
	}
	if strings.Contains(body, "2 stops (42s total) · 1 at lights #weirdstats") {
		t.Fatalf("expected managed weirdstats line to be hidden from description")
	}
}

func TestActivities_FallsBackToStoredDescriptionWhenOnlyManagedLineExists(t *testing.T) {
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
		UserID:      405,
		AccessToken: "token",
		AthleteID:   405,
		AthleteName: "Dina Example",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 16, 9, 0, 0, 0, time.UTC)
	_, err = store.InsertActivity(ctx, storage.Activity{
		UserID:      405,
		Type:        "Ride",
		Name:        "Managed Description Ride",
		StartTime:   start,
		Description: "2 stops (42s total) · 1 at lights #weirdstats",
	}, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: start, Speed: 6}})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activities/", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 405); err != nil {
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
	if !strings.Contains(body, "2 stops (42s total) · 1 at lights #weirdstats") {
		t.Fatalf("expected stored activity description in response")
	}
	if !strings.Contains(body, "2 detected facts") {
		t.Fatalf("expected detected fact count in response")
	}
}

func TestActivityDetail_ShowsMapLinkedFacts(t *testing.T) {
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
		UserID:      505,
		AccessToken: "token",
		AthleteID:   505,
		AthleteName: "Erin Example",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 17, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      505,
		Type:        "Ride",
		Name:        "Focused Detail Ride",
		StartTime:   start,
		Description: "",
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 8},
		{Lat: 52.5204, Lon: 13.4048, Time: start.Add(1 * time.Minute), Speed: 8},
		{Lat: 52.5208, Lon: 13.4056, Time: start.Add(2 * time.Minute), Speed: 8},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.ReplaceActivityStops(ctx, activityID, []storage.ActivityStop{
		{Seq: 0, Lat: 52.5204, Lon: 13.4048, StartSeconds: 60, DurationSeconds: 45, HasTrafficLight: true},
	}, time.Now()); err != nil {
		t.Fatalf("replace stops: %v", err)
	}
	factsJSON, err := json.Marshal([]ActivityMapFactView{
		{
			ID:      "longest_segment",
			Kind:    "segment",
			Title:   "Longest uninterrupted segment",
			Summary: "1.2 km at 28 km/h",
			Color:   "#ef4444",
			Path: []routePreviewPoint{
				{Lat: 52.5200, Lon: 13.4040},
				{Lat: 52.5208, Lon: 13.4056},
			},
		},
		{
			ID:      "stop_summary",
			Kind:    "cluster",
			Title:   "Stop summary",
			Summary: "1 detected stop",
			Color:   "#f5a524",
			Points: []ActivityFactPoint{
				{Lat: 52.5204, Lon: 13.4048},
			},
		},
		{
			ID:      "traffic_light_stops",
			Kind:    "cluster",
			Title:   "Traffic light stops",
			Summary: "1 stop at a light",
			Color:   "#ff3b30",
			Points: []ActivityFactPoint{
				{Lat: 52.5204, Lon: 13.4048},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal detected facts: %v", err)
	}
	if err := store.UpsertActivityDetectedFacts(ctx, activityID, string(factsJSON), time.Now()); err != nil {
		t.Fatalf("upsert detected facts: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{SpeedThreshold: 0.5}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10), nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 505); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.ActivityDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Route &amp; detected facts",
		"data-focus-fact=\"longest_segment\"",
		"data-focus-fact=\"stop_summary\"",
		"data-focus-fact=\"traffic_light_stops\"",
		"The map is the primary view.",
		"/activity/" + strconv.FormatInt(activityID, 10) + "/poster",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in detail response", want)
		}
	}
}

func TestActivityDetail_ShowsStoredDataInventory(t *testing.T) {
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
		UserID:      606,
		AccessToken: "token",
		AthleteID:   606,
		AthleteName: "Frank Example",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 23, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      606,
		Type:        "Ride",
		Name:        "No Stop Mystery",
		StartTime:   start,
		Description: "Short pause test",
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 7},
		{Lat: 52.5202, Lon: 13.4042, Time: start.Add(1 * time.Minute), Speed: 0},
		{Lat: 52.5202, Lon: 13.4042, Time: start.Add(1*time.Minute + 45*time.Second), Speed: 0},
		{Lat: 52.5205, Lon: 13.4046, Time: start.Add(2 * time.Minute), Speed: 7},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.UpsertActivityStats(ctx, activityID, stats.StopStats{
		StopCount:             0,
		StopTotalSeconds:      0,
		TrafficLightStopCount: 0,
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("upsert activity stats: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10), nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 606); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.ActivityDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"What Weirdstats has",
		"Route points",
		"Stop detection",
		"Coffee stop",
		"Stats snapshot",
		"Map-linked facts",
		"1 candidate low-speed window found; the longest lasted 45s",
		"Overpass-backed detection is unavailable",
		"0.5 m/s",
		"1m 0s",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in detail response", want)
		}
	}
}
