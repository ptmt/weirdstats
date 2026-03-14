package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/jobs"
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
