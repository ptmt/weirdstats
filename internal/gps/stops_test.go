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

func TestDetectStops_GlitchTolerance(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	// A stop with a brief speed glitch in the middle that should be ignored.
	points := []Point{
		{Lat: 1, Lon: 1, Time: base, Speed: 5},
		{Lat: 1, Lon: 1, Time: base.Add(30 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(60 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(65 * time.Second), Speed: 3},  // glitch: 5s spike
		{Lat: 1, Lon: 1, Time: base.Add(70 * time.Second), Speed: 0},  // back to stopped
		{Lat: 1, Lon: 1, Time: base.Add(120 * time.Second), Speed: 0},
		{Lat: 1, Lon: 1, Time: base.Add(150 * time.Second), Speed: 5},
	}

	// Without glitch tolerance: glitch splits the stop into two short segments, neither >= 1min.
	stops := DetectStops(points, StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute})
	if len(stops) != 0 {
		t.Fatalf("without glitch tolerance: expected 0 stops, got %d", len(stops))
	}

	// With glitch tolerance: the 5s spike is ignored, producing one 90s stop.
	stops = DetectStops(points, StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute, GlitchTolerance: 10 * time.Second})
	if len(stops) != 1 {
		t.Fatalf("with glitch tolerance: expected 1 stop, got %d", len(stops))
	}
	if got := stops[0].Duration; got != 90*time.Second {
		t.Fatalf("expected stop duration 90s, got %s", got)
	}
}
