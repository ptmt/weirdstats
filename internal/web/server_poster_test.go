package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestActivityPoster_RendersStoredDetectedFacts(t *testing.T) {
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
		UserID:      707,
		AccessToken: "token",
		AthleteID:   707,
		AthleteName: "Poster Rider",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 24, 7, 30, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:    707,
		Type:      "Ride",
		Name:      "Poster Route",
		StartTime: start,
		Distance:  18420,
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 7},
		{Lat: 52.5205, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 8},
		{Lat: 52.5210, Lon: 13.4062, Time: start.Add(4 * time.Minute), Speed: 7},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.ReplaceActivityStops(ctx, activityID, []storage.ActivityStop{
		{Seq: 0, Lat: 52.5205, Lon: 13.4050, StartSeconds: 120, DurationSeconds: 35},
	}, time.Now()); err != nil {
		t.Fatalf("replace stops: %v", err)
	}

	factsJSON, err := json.Marshal([]ActivityMapFactView{
		{
			ID:      weirdStatsFactLongestSegment,
			Kind:    "segment",
			Title:   "Longest uninterrupted segment",
			Summary: "3.2 km without a real stop",
			Color:   "#22c55e",
			Path: []routePreviewPoint{
				{Lat: 52.5200, Lon: 13.4040},
				{Lat: 52.5210, Lon: 13.4062},
			},
		},
		{
			ID:      weirdStatsFactStopSummary,
			Kind:    "collection",
			Title:   "Stop summary",
			Summary: "1 detected stop",
			Color:   "#ec4899",
			Points: []ActivityFactPoint{
				{Lat: 52.5205, Lon: 13.4050},
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

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10)+"/poster", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 707); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Activity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected html content type, got %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"WEIRDSTATS SHARE CARD",
		"Route poster for Poster Route",
		"Longest uninterrupted segment",
		"3.2 km without a real stop",
		"Stop summary",
		"Numbers on the map match the fact cards",
		"/activity/" + strconv.FormatInt(activityID, 10) + "/poster.png",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in poster response", want)
		}
	}
}

func TestActivityPoster_FallsBackToRebuiltFactsWhenCacheMissing(t *testing.T) {
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
		UserID:      808,
		AccessToken: "token",
		AthleteID:   808,
		AthleteName: "Fallback Rider",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 24, 9, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:    808,
		Type:      "Ride",
		Name:      "Fallback Route",
		StartTime: start,
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 7},
		{Lat: 52.5204, Lon: 13.4048, Time: start.Add(1 * time.Minute), Speed: 0},
		{Lat: 52.5208, Lon: 13.4056, Time: start.Add(2 * time.Minute), Speed: 7},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.ReplaceActivityStops(ctx, activityID, []storage.ActivityStop{
		{
			Seq:             0,
			Lat:             52.5204,
			Lon:             13.4048,
			StartSeconds:    60,
			DurationSeconds: 40,
			HasRoadCrossing: true,
			CrossingRoad:    "Broadway",
		},
	}, time.Now()); err != nil {
		t.Fatalf("replace stops: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{SpeedThreshold: 0.5}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10)+"/poster", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 808); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Activity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Road crossings",
		"Broadway",
		"Stop summary",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in poster response", want)
		}
	}
}

func TestActivityPosterPNG_RendersImage(t *testing.T) {
	origCapture := posterPNGCapture
	defer func() {
		posterPNGCapture = origCapture
	}()

	wantPNG := tinyPosterPNG(t)
	posterPNGCapture = func(_ context.Context, html []byte) ([]byte, error) {
		for _, want := range [][]byte{
			[]byte("PNG Route"),
			[]byte("WEIRDSTATS SHARE CARD"),
			[]byte("poster-export"),
		} {
			if !bytes.Contains(html, want) {
				t.Fatalf("expected rendered html to contain %q", string(want))
			}
		}
		return wantPNG, nil
	}

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
		UserID:      909,
		AccessToken: "token",
		AthleteID:   909,
		AthleteName: "PNG Rider",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 24, 10, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:    909,
		Type:      "Ride",
		Name:      "PNG Route",
		StartTime: start,
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 7},
		{Lat: 52.5206, Lon: 13.4052, Time: start.Add(2 * time.Minute), Speed: 8},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{SpeedThreshold: 0.5}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10)+"/poster.png", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 909); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, cookie := range sessionRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()

	server.Activity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected png content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "weirdstats-activity-"+strconv.FormatInt(activityID, 10)+"-poster.png") {
		t.Fatalf("unexpected content disposition: %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), wantPNG) {
		t.Fatalf("unexpected png body")
	}
}

func tinyPosterPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 217, G: 93, B: 57, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
