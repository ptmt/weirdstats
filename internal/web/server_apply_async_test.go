package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/jobs"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestApplyActivityRules_EnqueuesJob(t *testing.T) {
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
		UserID:      1,
		AccessToken: "token",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 13, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      1,
		Type:        "Ride",
		Name:        "Queued Apply",
		StartTime:   start,
		Description: "",
	}, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: start, Speed: 6}})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/activity/%d/apply", activityID), nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 1); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.ApplyActivityRules(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, fmt.Sprintf("/activity/%d", activityID)) || !strings.Contains(location, "sync%2Bqueued") {
		t.Fatalf("unexpected redirect location: %q", location)
	}

	jobRows, err := store.ListJobsByType(ctx, jobs.JobTypeApplyActivityRules, 10)
	if err != nil {
		t.Fatalf("list apply jobs: %v", err)
	}
	if len(jobRows) != 1 {
		t.Fatalf("expected 1 apply job, got %d", len(jobRows))
	}
	if !strings.Contains(jobRows[0].Payload, fmt.Sprintf("\"activity_id\":%d", activityID)) {
		t.Fatalf("unexpected payload: %q", jobRows[0].Payload)
	}
}

func TestApply_CachesDetectedFactsForDetailPage(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	disabledPrefs := make([]storage.UserFactPreference, 0, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		disabledPrefs = append(disabledPrefs, storage.UserFactPreference{
			UserID:  1,
			FactID:  def.ID,
			Enabled: false,
		})
	}
	if err := store.ReplaceUserFactPreferences(ctx, 1, disabledPrefs); err != nil {
		t.Fatalf("replace fact preferences: %v", err)
	}

	start := time.Date(2026, time.March, 23, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      1,
		Type:        "Ride",
		Name:        "Cached Facts Ride",
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
	if err := store.UpsertActivityStats(ctx, activityID, stats.StopStats{
		StopCount:             1,
		StopTotalSeconds:      90,
		TrafficLightStopCount: 1,
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("upsert activity stats: %v", err)
	}
	if err := store.ReplaceActivityStops(ctx, activityID, []storage.ActivityStop{
		{Seq: 0, Lat: 52.5204, Lon: 13.4048, StartSeconds: 60, DurationSeconds: 90, HasTrafficLight: true},
	}, time.Now()); err != nil {
		t.Fatalf("replace stops: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if err := server.Apply(ctx, activityID); err != nil {
		t.Fatalf("apply: %v", err)
	}

	rawFacts, _, err := store.GetActivityDetectedFacts(ctx, activityID)
	if err != nil {
		t.Fatalf("get detected facts: %v", err)
	}
	var detectedFacts []ActivityMapFactView
	if err := json.Unmarshal([]byte(rawFacts), &detectedFacts); err != nil {
		t.Fatalf("unmarshal detected facts: %v", err)
	}

	seen := make(map[string]bool, len(detectedFacts))
	for _, fact := range detectedFacts {
		seen[fact.ID] = true
	}
	for _, want := range []string{"longest_segment", "stop_summary", "traffic_light_stops"} {
		if !seen[want] {
			t.Fatalf("expected cached detected fact %q, got %+v", want, detectedFacts)
		}
	}
}
