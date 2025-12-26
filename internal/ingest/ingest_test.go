package ingest

import (
	"testing"
	"time"

	"weirdstats/internal/strava"
)

func TestBuildPoints(t *testing.T) {
	start := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	streams := strava.StreamSet{
		LatLng:         [][2]float64{{1, 2}, {3, 4}},
		TimeOffsetsSec: []int{0, 60},
		VelocitySmooth: []float64{1.1, 2.2},
	}

	points, err := buildPoints(start, streams)
	if err != nil {
		t.Fatalf("build points: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	if points[1].Time != start.Add(60*time.Second) {
		t.Fatalf("unexpected time: %s", points[1].Time)
	}
	if points[1].Speed != 2.2 {
		t.Fatalf("unexpected speed: %v", points[1].Speed)
	}
}
