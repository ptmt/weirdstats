package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/storage"
)

type stubMapAPI struct {
	features []maps.Feature
	calls    int
}

func (s *stubMapAPI) NearbyFeatures(lat, lon float64) ([]maps.Feature, error) {
	s.calls++
	return s.features, nil
}

func TestStopStatsProcessor_ComputesStopsAndTrafficLights(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	points := []gps.Point{
		{Lat: 40.0, Lon: -73.0, Time: now, Speed: 3.0},
		{Lat: 40.0, Lon: -73.0, Time: now.Add(10 * time.Second), Speed: 3.0},
		{Lat: 40.0, Lon: -73.0, Time: now.Add(20 * time.Second), Speed: 0.0},       // stop start
		{Lat: 40.0, Lon: -73.0, Time: now.Add(50 * time.Second), Speed: 0.0},       // still stopped
		{Lat: 40.0, Lon: -73.0, Time: now.Add(60 * time.Second), Speed: 2.0},       // moving resumes
		{Lat: 40.0001, Lon: -73.0001, Time: now.Add(70 * time.Second), Speed: 3.0}, // moving
	}

	activityID, err := store.InsertActivity(context.Background(), storage.Activity{
		UserID:      1,
		Type:        "Ride",
		Name:        "Test Activity",
		StartTime:   now,
		Description: "test",
		Distance:    1000,
		MovingTime:  70,
	}, points)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	mapStub := &stubMapAPI{features: []maps.Feature{{Type: maps.FeatureTrafficLight}}}
	processor := &StopStatsProcessor{
		Store:   store,
		MapAPI:  mapStub,
		Options: gps.StopOptions{SpeedThreshold: 0.5, MinDuration: 30 * time.Second},
	}

	if err := processor.Process(context.Background(), activityID); err != nil {
		t.Fatalf("process: %v", err)
	}

	stats, err := store.GetActivityStats(context.Background(), activityID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats.StopCount != 1 {
		t.Fatalf("expected 1 stop, got %d", stats.StopCount)
	}
	if stats.StopTotalSeconds != 30 {
		t.Fatalf("expected 30s stop total, got %d", stats.StopTotalSeconds)
	}
	if stats.TrafficLightStopCount != 1 {
		t.Fatalf("expected 1 traffic light stop, got %d", stats.TrafficLightStopCount)
	}
	if mapStub.calls != 1 {
		t.Fatalf("expected 1 map lookup, got %d", mapStub.calls)
	}
}

type activityFixture struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	StartTime   string  `json:"start_time"`
	Description string  `json:"description"`
	Distance    float64 `json:"distance"`
	MovingTime  int     `json:"moving_time"`
	Points      []struct {
		Lat   float64 `json:"lat"`
		Lon   float64 `json:"lon"`
		Time  string  `json:"time"`
		Speed float64 `json:"speed"`
	} `json:"points"`
}

func loadActivityFixture(t *testing.T, path string) (storage.Activity, []gps.Point) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx activityFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	start, err := time.Parse(time.RFC3339, fx.StartTime)
	if err != nil {
		t.Fatalf("parse start time: %v", err)
	}
	var points []gps.Point
	for i, p := range fx.Points {
		ts, err := time.Parse(time.RFC3339, p.Time)
		if err != nil {
			t.Fatalf("parse point %d time: %v", i, err)
		}
		points = append(points, gps.Point{
			Lat:   p.Lat,
			Lon:   p.Lon,
			Time:  ts,
			Speed: p.Speed,
		})
	}
	activity := storage.Activity{
		ID:          fx.ID,
		UserID:      1,
		Type:        fx.Type,
		Name:        fx.Name,
		StartTime:   start,
		Description: fx.Description,
		Distance:    fx.Distance,
		MovingTime:  fx.MovingTime,
	}
	return activity, points
}

func TestStopStatsProcessor_WithSampleActivityFixture(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fixture.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	activity, points := loadActivityFixture(t, filepath.Join(repoRoot(t), "testdata", "activities", "ride_sample.json"))
	activityID, err := store.InsertActivity(context.Background(), activity, points)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	mapStub := &stubMapAPI{features: []maps.Feature{{Type: maps.FeatureTrafficLight}}}
	processor := &StopStatsProcessor{
		Store:   store,
		MapAPI:  mapStub,
		Options: gps.StopOptions{SpeedThreshold: 0.5, MinDuration: 30 * time.Second},
	}

	if err := processor.Process(context.Background(), activityID); err != nil {
		t.Fatalf("process fixture: %v", err)
	}

	got, err := store.GetActivityStats(context.Background(), activityID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if got.StopCount != 5 {
		t.Fatalf("expected 5 stops (>=30s) from fixture, got %d", got.StopCount)
	}
	if got.StopTotalSeconds != 3316 {
		t.Fatalf("expected 3316 total stop seconds, got %d", got.StopTotalSeconds)
	}
	if got.TrafficLightStopCount != 5 {
		t.Fatalf("expected 5 traffic light stops from stub, got %d", got.TrafficLightStopCount)
	}
	if mapStub.calls != 5 {
		t.Fatalf("expected 5 map lookups, got %d", mapStub.calls)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

type recordedStop struct {
	Lat               float64        `json:"lat"`
	Lon               float64        `json:"lon"`
	DurationSeconds   float64        `json:"duration_seconds"`
	NearbyTrafficInfo []maps.Feature `json:"nearby_features"`
}

type overpassRecording struct {
	OverpassURL        string         `json:"overpass_url"`
	SpeedThreshold     float64        `json:"speed_threshold"`
	MinDurationSeconds int            `json:"min_duration_seconds"`
	Stops              []recordedStop `json:"stops"`
}

// Integration helper: set RECORD_OVERPASS=1 to run, requires network access.
func TestRecordOverpassForFixture(t *testing.T) {
	if os.Getenv("RECORD_OVERPASS") == "" {
		t.Skip("set RECORD_OVERPASS=1 to record live Overpass responses")
	}

	overpassURL := os.Getenv("OVERPASS_URL")
	if overpassURL == "" {
		overpassURL = maps.DefaultOverpassURL
	}

	dbPath := filepath.Join(t.TempDir(), "record.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	activity, points := loadActivityFixture(t, filepath.Join(repoRoot(t), "testdata", "activities", "ride_sample.json"))
	activityID, err := store.InsertActivity(context.Background(), activity, points)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	client := &maps.OverpassClient{
		BaseURL:      overpassURL,
		DisableCache: true,
		Timeout:      25 * time.Second,
	}

	opts := gps.StopOptions{SpeedThreshold: 0.5, MinDuration: 30 * time.Second}
	pointsFromDB, err := store.LoadActivityPoints(context.Background(), activityID)
	if err != nil {
		t.Fatalf("load points: %v", err)
	}
	stops := gps.DetectStops(pointsFromDB, opts)

	var rec overpassRecording
	rec.OverpassURL = overpassURL
	rec.SpeedThreshold = opts.SpeedThreshold
	rec.MinDurationSeconds = int(opts.MinDuration.Seconds())

	for _, stop := range stops {
		features, err := client.NearbyFeatures(stop.Lat, stop.Lon)
		if err != nil {
			t.Fatalf("overpass query failed: %v", err)
		}
		rec.Stops = append(rec.Stops, recordedStop{
			Lat:               stop.Lat,
			Lon:               stop.Lon,
			DurationSeconds:   stop.Duration.Seconds(),
			NearbyTrafficInfo: features,
		})
	}

	outputPath := filepath.Join(repoRoot(t), "tmp", "overpass_recordings", "ride_sample.json")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatalf("mkdir tmp recordings: %v", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal recording: %v", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	t.Logf("recorded %d stops to %s", len(rec.Stops), outputPath)
}
