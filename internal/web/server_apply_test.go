package web

import (
	"strings"
	"testing"

	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestApplyWeirdStatsDescription(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             3,
		StopTotalSeconds:      95,
		TrafficLightStopCount: 2,
	}
	line := "Weirdstats: 3 stops (1m 35s total) · 2 at lights"

	tests := []struct {
		name     string
		existing string
		stats    stats.StopStats
		want     string
		changed  bool
	}{
		{
			name:     "appends to empty description",
			existing: "",
			stats:    snapshot,
			want:     line,
			changed:  true,
		},
		{
			name:     "appends after existing text",
			existing: "Morning ride with intervals",
			stats:    snapshot,
			want:     "Morning ride with intervals\n\n" + line,
			changed:  true,
		},
		{
			name:     "replaces previous weirdstats line and keeps paragraphs",
			existing: "First paragraph.\n\nSecond paragraph.\nWeirdstats: 1 stops (12s total)",
			stats:    snapshot,
			want:     "First paragraph.\n\nSecond paragraph.\n\n" + line,
			changed:  true,
		},
		{
			name:     "no change when same line already present",
			existing: "Morning ride with intervals\n\n" + line,
			stats:    snapshot,
			want:     "Morning ride with intervals\n\n" + line,
			changed:  false,
		},
		{
			name:     "no stats keeps description unchanged",
			existing: "Plain description",
			stats:    stats.StopStats{},
			want:     "Plain description",
			changed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := applyWeirdStatsDescription(tt.existing, tt.stats)
			if got != tt.want {
				t.Fatalf("unexpected description\nwant: %q\n got: %q", tt.want, got)
			}
			if changed != tt.changed {
				t.Fatalf("unexpected changed flag: want %v got %v", tt.changed, changed)
			}
		})
	}
}

func TestStopStatsFromStops(t *testing.T) {
	stops := []storage.ActivityStop{
		{DurationSeconds: 20, HasTrafficLight: true},
		{DurationSeconds: 35, HasTrafficLight: false},
		{DurationSeconds: 15, HasTrafficLight: true},
	}

	got := stopStatsFromStops(stops)
	if got.StopCount != 3 {
		t.Fatalf("expected 3 stops, got %d", got.StopCount)
	}
	if got.StopTotalSeconds != 70 {
		t.Fatalf("expected 70 total seconds, got %d", got.StopTotalSeconds)
	}
	if got.TrafficLightStopCount != 2 {
		t.Fatalf("expected 2 traffic-light stops, got %d", got.TrafficLightStopCount)
	}
}

func TestBuildRoutePreviewPath(t *testing.T) {
	points := []storage.ActivityRoutePoint{
		{Lat: 37.7788, Lon: -122.4350},
		{Lat: 37.7762, Lon: -122.4269},
		{Lat: 37.7701, Lon: -122.4213},
		{Lat: 37.7685, Lon: -122.4120},
	}

	path, startX, startY, endX, endY, ok := buildRoutePreviewPath(points, 188, 62, 8)
	if !ok {
		t.Fatalf("expected route preview path")
	}
	if path == "" {
		t.Fatalf("expected non-empty svg path")
	}
	if !strings.HasPrefix(path, "M ") {
		t.Fatalf("expected path to start with move command, got %q", path)
	}
	if !strings.Contains(path, " L ") {
		t.Fatalf("expected line segments in path, got %q", path)
	}
	if startX == endX && startY == endY {
		t.Fatalf("expected distinct start/end points")
	}
	if startX < 0 || startX > 188 || endX < 0 || endX > 188 {
		t.Fatalf("x coordinates out of bounds: start=%f end=%f", startX, endX)
	}
	if startY < 0 || startY > 62 || endY < 0 || endY > 62 {
		t.Fatalf("y coordinates out of bounds: start=%f end=%f", startY, endY)
	}
}

func TestBuildRoutePreviewPathRejectsSinglePoint(t *testing.T) {
	_, _, _, _, _, ok := buildRoutePreviewPath([]storage.ActivityRoutePoint{{Lat: 1, Lon: 1}}, 188, 62, 8)
	if ok {
		t.Fatalf("expected single-point route to be rejected")
	}
}
