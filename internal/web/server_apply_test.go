package web

import (
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
