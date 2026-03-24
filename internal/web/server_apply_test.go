package web

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestLoadStoredCoffeeStopFact(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      1,
		Type:        "Ride",
		Name:        "Coffee Cache",
		StartTime:   start,
		Description: "",
	}, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: start, Speed: 6}})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	factsJSON, err := json.Marshal([]ActivityMapFactView{
		{
			ID:      weirdStatsFactCoffeeStop,
			Title:   "Coffee stop",
			Summary: "Bean Machine",
			Points: []ActivityFactPoint{
				{Lat: 52.52031, Lon: 13.40501},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal facts: %v", err)
	}
	if err := store.UpsertActivityDetectedFacts(ctx, activityID, string(factsJSON), time.Now()); err != nil {
		t.Fatalf("upsert detected facts: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	got, err := server.loadStoredCoffeeStopFact(ctx, activityID)
	if err != nil {
		t.Fatalf("loadStoredCoffeeStopFact: %v", err)
	}
	if got.Name != "Bean Machine" || !got.HasLocation {
		t.Fatalf("unexpected coffee fact: %+v", got)
	}
	if got.Lat != 52.52031 || got.Lon != 13.40501 {
		t.Fatalf("unexpected coffee coordinates: %+v", got)
	}
}
