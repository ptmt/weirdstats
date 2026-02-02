package worker

import (
	"context"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/jobs"
	"weirdstats/internal/maps"
	"weirdstats/internal/processor"
	"weirdstats/internal/storage"
)

type fakeMapAPI struct{}

func (f fakeMapAPI) NearbyFeatures(lat, lon float64) ([]maps.Feature, error) {
	if lat == 1 {
		return []maps.Feature{{Type: maps.FeatureTrafficLight, Name: "Main St"}}, nil
	}
	return nil, nil
}

func TestWorkerProcessesQueue(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 0, Lon: 0, Time: base, Speed: 5},
		{Lat: 1, Lon: 1, Time: base.Add(60 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(120 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(180 * time.Second), Speed: 5},
		{Lat: 2, Lon: 2, Time: base.Add(240 * time.Second), Speed: 0},
		{Lat: 2, Lon: 2, Time: base.Add(300 * time.Second), Speed: 0},
		{Lat: 2, Lon: 2, Time: base.Add(360 * time.Second), Speed: 5},
	}

	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      1,
		Type:        "Ride",
		Name:        "Morning Ride",
		StartTime:   base,
		Description: "",
	}, points)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	if err := jobs.EnqueueProcessActivity(ctx, store, activityID); err != nil {
		t.Fatalf("enqueue activity: %v", err)
	}

	statsProcessor := &processor.StopStatsProcessor{
		Store:   store,
		MapAPI:  fakeMapAPI{},
		Options: gps.StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute},
	}

	runner := &jobs.Runner{Store: store, Processor: statsProcessor}
	processed, err := runner.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("process next: %v", err)
	}
	if !processed {
		t.Fatalf("expected queue item to be processed")
	}

	stats, err := store.GetActivityStats(ctx, activityID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats.StopCount != 2 {
		t.Fatalf("expected 2 stops, got %d", stats.StopCount)
	}
	if stats.StopTotalSeconds != 120 {
		t.Fatalf("expected total stop seconds 120, got %d", stats.StopTotalSeconds)
	}
	if stats.TrafficLightStopCount != 1 {
		t.Fatalf("expected traffic light stop count 1, got %d", stats.TrafficLightStopCount)
	}
}
