package storage

import (
	"context"
	"testing"
	"time"

	"weirdstats/internal/gps"
)

func TestActivityPointsRoundTrip_WithPowerAndGrade(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, Activity{
		UserID:    1,
		Type:      "Ride",
		Name:      "Point Round Trip",
		StartTime: start,
	}, []gps.Point{
		{Lat: 52.52, Lon: 13.405, Time: start, Speed: 5, Power: 210, HasPower: true, Grade: -6.5, HasGrade: true},
		{Lat: 52.53, Lon: 13.406, Time: start.Add(30 * time.Second), Speed: 8},
	})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	points, err := store.LoadActivityPoints(ctx, activityID)
	if err != nil {
		t.Fatalf("load points: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	if !points[0].HasPower || points[0].Power != 210 {
		t.Fatalf("expected first point power to round-trip, got %+v", points[0])
	}
	if !points[0].HasGrade || points[0].Grade != -6.5 {
		t.Fatalf("expected first point grade to round-trip, got %+v", points[0])
	}
	if points[1].HasPower || points[1].HasGrade {
		t.Fatalf("expected second point to have no optional streams, got %+v", points[1])
	}
}
