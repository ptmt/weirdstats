package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/jobs"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

func TestVerifyActivityPowerFetchesSourceImmediately(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	if _, err := store.InsertActivity(ctx, storage.Activity{
		ID: 42, UserID: 11, Type: "Ride", Name: "TTT", StartTime: start, StreamsFetched: true,
	}, []gps.Point{{Lat: 48, Lon: 11, Time: start, Power: 200, HasPower: true}}); err != nil {
		t.Fatal(err)
	}
	var activityCalls, streamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/activities/42":
			activityCalls++
			fmt.Fprint(w, `{"id":42,"name":"TTT","type":"Ride","start_date":"2026-09-20T09:00:00Z","device_watts":true}`)
		case "/api/activities/42/streams":
			streamCalls++
			fmt.Fprint(w, `{"latlng":{"data":[[48,11],[48,11.001]]},"time":{"data":[0,10]},"watts":{"data":[200,210]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s, err := NewServer(store, &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL + "/api"}}, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/activity/42/verify-power", nil)
	sessionRec := httptest.NewRecorder()
	if err := s.setSession(sessionRec, req, 11); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Activity(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/activity/42" {
		t.Fatalf("unexpected power check response: %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if activityCalls != 1 || streamCalls != 0 {
		t.Fatalf("power check fetched wrong data: activity=%d streams=%d", activityCalls, streamCalls)
	}
	activity, err := store.GetActivityForUser(ctx, 11, 42)
	if err != nil {
		t.Fatal(err)
	}
	if activity.DeviceWatts == nil || !*activity.DeviceWatts || !activity.PowerSourceChecked {
		t.Fatalf("power source not refreshed: %+v", activity)
	}
	points, err := store.LoadActivityPoints(ctx, 42)
	if err != nil || len(points) != 1 {
		t.Fatalf("power check changed streams: %v, %d points", err, len(points))
	}
}

func TestRefreshActivityQueuesStravaRefetch(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertActivity(ctx, storage.Activity{ID: 42, UserID: 11, Type: "Ride", Name: "TTT", StartTime: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/activity/42/refresh", nil)
	sessionRec := httptest.NewRecorder()
	if err := s.setSession(sessionRec, req, 11); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.RefreshActivity(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("refresh response: %d: %s", rec.Code, rec.Body.String())
	}
	queued, err := store.ListJobs(ctx, 10)
	if err != nil || len(queued) != 1 {
		t.Fatalf("refresh job missing: %v %+v", err, queued)
	}
	var payload jobs.ProcessActivityPayload
	if err := json.Unmarshal([]byte(queued[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Refetch || payload.ActivityID != 42 || payload.UserID != 11 {
		t.Fatalf("wrong refresh job: %+v", payload)
	}
}
