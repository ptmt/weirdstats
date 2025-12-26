package gps

import (
	"testing"
	"time"
)

func TestDetectStops(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	points := []Point{
		{Lat: 1, Lon: 1, Time: base, Speed: 5},
		{Lat: 1, Lon: 1, Time: base.Add(30 * time.Second), Speed: 5},
		{Lat: 1, Lon: 1, Time: base.Add(60 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(120 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(180 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(210 * time.Second), Speed: 5},
		{Lat: 2, Lon: 2, Time: base.Add(240 * time.Second), Speed: 0},
		{Lat: 2, Lon: 2, Time: base.Add(250 * time.Second), Speed: 0},
		{Lat: 2, Lon: 2, Time: base.Add(260 * time.Second), Speed: 5},
	}

	stops := DetectStops(points, StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute})
	if len(stops) != 1 {
		t.Fatalf("expected 1 stop, got %d", len(stops))
	}
	if got := stops[0].Duration; got != 120*time.Second {
		t.Fatalf("expected stop duration 120s, got %s", got)
	}
}
