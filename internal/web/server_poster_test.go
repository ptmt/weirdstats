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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
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
		"WeirdStats Share Card",
		"Route poster for Poster Route",
		"Longest uninterrupted segment",
		"3.2 km without a real stop",
		"Stop summary",
		"story-map-stats",
		"Distance",
		"Export PNG",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in poster response", want)
		}
	}
	for _, unwanted := range []string{
		"Selected Facts",
		"Download PNG",
		"Route + Fact Anchors",
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("did not expect %q in poster response", unwanted)
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

func TestActivityPoster_AppliesRenderOptions(t *testing.T) {
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
		UserID:      818,
		AccessToken: "token",
		AthleteID:   818,
		AthleteName: "Options Rider",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 24, 9, 30, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:    818,
		Type:      "Ride",
		Name:      "Option Route",
		StartTime: start,
	}, []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 7},
		{Lat: 52.5205, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 8},
		{Lat: 52.5210, Lon: 13.4062, Time: start.Add(4 * time.Minute), Speed: 7},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
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

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10)+"/poster?header=0&meta=0&facts=1&transparent=1&uppercase=1&mono=1", nil)
	sessionRec := httptest.NewRecorder()
	if err := server.setSession(sessionRec, req, 818); err != nil {
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
		"story-shot--no-header",
		"story-shot--transparent",
		"story-shot--uppercase",
		"story-shot--mono",
		"story-map-stats",
		"Export PNG",
		"Longest uninterrupted segment",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in poster response", want)
		}
	}
	for _, unwanted := range []string{
		"WEIRDSTATS SHARE CARD",
		"Ride ·",
		"Stop summary",
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("did not expect %q in poster response", unwanted)
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
			[]byte("poster-export"),
			[]byte("story-shot--transparent"),
			[]byte("story-shot--uppercase"),
			[]byte("story-shot--mono"),
		} {
			if !bytes.Contains(html, want) {
				t.Fatalf("expected rendered html to contain %q", string(want))
			}
		}
		for _, unwanted := range [][]byte{
			[]byte("WEIRDSTATS SHARE CARD"),
			[]byte("Stop summary"),
		} {
			if bytes.Contains(html, unwanted) {
				t.Fatalf("did not expect rendered html to contain %q", string(unwanted))
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

	req := httptest.NewRequest(http.MethodGet, "/activity/"+strconv.FormatInt(activityID, 10)+"/poster.png?header=0&meta=0&facts=0&transparent=1&uppercase=1&mono=1", nil)
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

func TestFindPosterBrowser_ReportsProbeSummary(t *testing.T) {
	origCandidates := posterBrowserCandidates
	defer func() {
		posterBrowserCandidates = origCandidates
	}()

	tempDir := t.TempDir()
	browserPath := filepath.Join(tempDir, "fake-browser")
	if err := os.WriteFile(browserPath, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}

	posterBrowserCandidates = []string{
		"definitely-not-a-real-browser-for-weirdstats",
		browserPath,
	}

	gotPath, gotProbe, err := findPosterBrowser()
	if err != nil {
		t.Fatalf("find poster browser: %v", err)
	}
	if gotPath != browserPath {
		t.Fatalf("expected browser path %q, got %q", browserPath, gotPath)
	}
	if !strings.Contains(gotProbe, "definitely-not-a-real-browser-for-weirdstats:miss") {
		t.Fatalf("expected miss entry in probe summary, got %q", gotProbe)
	}
	if !strings.Contains(gotProbe, posterBrowserProbeLabel(browserPath)+":hit") {
		t.Fatalf("expected hit entry in probe summary, got %q", gotProbe)
	}
}

func TestSelectPosterWaterways_PrioritizesNamedRiverNearRoute(t *testing.T) {
	routePoints := []gps.Point{
		{Lat: 48.1370, Lon: 11.5750},
		{Lat: 48.1378, Lon: 11.5762},
		{Lat: 48.1386, Lon: 11.5774},
	}

	waterways := []maps.PolylineFeature{
		{
			Name: "Small Stream",
			Kind: "stream",
			Geometry: []maps.LatLon{
				{Lat: 48.1371, Lon: 11.5752},
				{Lat: 48.1379, Lon: 11.5764},
			},
		},
		{
			Name: "Isar",
			Kind: "river",
			Geometry: []maps.LatLon{
				{Lat: 48.1368, Lon: 11.5748},
				{Lat: 48.1388, Lon: 11.5770},
			},
		},
		{
			Name: "Far Canal",
			Kind: "canal",
			Geometry: []maps.LatLon{
				{Lat: 48.1450, Lon: 11.5900},
				{Lat: 48.1460, Lon: 11.5910},
			},
		},
	}

	selected := selectPosterWaterways(waterways, routePoints, 2)
	if len(selected) != 2 {
		t.Fatalf("expected 2 waterways, got %d", len(selected))
	}
	if selected[0].Name != "Isar" {
		t.Fatalf("expected Isar to be prioritized, got %+v", selected)
	}
	if selected[1].Name != "Small Stream" {
		t.Fatalf("expected Small Stream to remain after Isar, got %+v", selected)
	}
}

func TestBuildPosterStats_SimplifiesOverlayCopy(t *testing.T) {
	start := time.Date(2026, time.March, 24, 7, 30, 0, 0, time.UTC)
	stats := buildPosterStats(storage.Activity{
		Type:       "Ride",
		Distance:   18420,
		MovingTime: 2700,
		StartTime:  start,
	}, []storage.ActivityStop{
		{DurationSeconds: 35, HasTrafficLight: true, HasRoadCrossing: true},
	})

	if len(stats) != 4 {
		t.Fatalf("expected 4 stats, got %d", len(stats))
	}
	if stats[0].Label != "Distance" {
		t.Fatalf("expected first stat label %q, got %q", "Distance", stats[0].Label)
	}
	if stats[1].Label != "Speed" {
		t.Fatalf("expected speed label to be simplified, got %q", stats[1].Label)
	}
	if stats[2].Label != "Time" {
		t.Fatalf("expected moving time label to be simplified, got %q", stats[2].Label)
	}
	if stats[3].Label != "Stops" || stats[3].Value != "1" {
		t.Fatalf("unexpected stop stat: %+v", stats[3])
	}
	if stats[3].Unit != "" || stats[3].Detail != "" {
		t.Fatalf("expected stop stat copy to stay minimal, got %+v", stats[3])
	}
}

func TestNewPosterProjectionFromBBox_KeepsContextWithinCanvas(t *testing.T) {
	routeProj, ok := newPosterProjection([]routePreviewPoint{
		{Lat: 52.5200, Lon: 13.4040},
		{Lat: 52.5207, Lon: 13.4050},
	}, posterMapWidth, posterMapHeight, posterMapPadding)
	if !ok {
		t.Fatal("expected route projection")
	}

	bboxProj, ok := newPosterProjectionFromBBox(maps.BBox{
		South: 52.5194,
		West:  13.4028,
		North: 52.5213,
		East:  13.4063,
	}, posterMapWidth, posterMapHeight, posterMapPadding)
	if !ok {
		t.Fatal("expected bbox projection")
	}

	lat := 52.5204
	lon := 13.4030
	oldX, _ := routeProj.project(lat, lon)
	if oldX >= 0 {
		t.Fatalf("expected route-only projection to push context off canvas, got x=%.1f", oldX)
	}

	x, y := bboxProj.project(lat, lon)
	if x < posterMapPadding || x > posterMapWidth-posterMapPadding {
		t.Fatalf("expected projected x within padded canvas, got %.1f", x)
	}
	if y < posterMapPadding || y > posterMapHeight-posterMapPadding {
		t.Fatalf("expected projected y within padded canvas, got %.1f", y)
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
