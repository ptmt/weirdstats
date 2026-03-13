package web

import (
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestApplyWeirdStatsDescription(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             3,
		StopTotalSeconds:      95,
		TrafficLightStopCount: 2,
	}
	rideFact := rideSegmentFact{
		DistanceMeters: 48000,
		AvgPower:       200,
		AvgSpeedMPS:    30.0 / 3.6,
	}
	line := "Longest uninterrupted segment: 48km - 200w - 30kmh · 3 stops (1m 35s total) · 2 at lights #weirdstats"

	tests := []struct {
		name     string
		existing string
		stats    stats.StopStats
		rideFact rideSegmentFact
		want     string
		changed  bool
	}{
		{
			name:     "appends to empty description",
			existing: "",
			stats:    snapshot,
			rideFact: rideFact,
			want:     line,
			changed:  true,
		},
		{
			name:     "appends after existing text",
			existing: "Morning ride with intervals",
			stats:    snapshot,
			rideFact: rideFact,
			want:     "Morning ride with intervals\n\n" + line,
			changed:  true,
		},
		{
			name:     "replaces previous weirdstats line and keeps paragraphs",
			existing: "First paragraph.\n\nSecond paragraph.\nWeirdstats: 1 stops (12s total)",
			stats:    snapshot,
			rideFact: rideFact,
			want:     "First paragraph.\n\nSecond paragraph.\n\n" + line,
			changed:  true,
		},
		{
			name:     "no change when same line already present",
			existing: "Morning ride with intervals\n\n" + line,
			stats:    snapshot,
			rideFact: rideFact,
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
			got, changed := applyWeirdStatsDescription(tt.existing, tt.stats, tt.rideFact)
			if got != tt.want {
				t.Fatalf("unexpected description\nwant: %q\n got: %q", tt.want, got)
			}
			if changed != tt.changed {
				t.Fatalf("unexpected changed flag: want %v got %v", tt.changed, changed)
			}
		})
	}
}

func TestApplyWeirdStatsDescription_WithRideFactOnly(t *testing.T) {
	rideFact := rideSegmentFact{
		DistanceMeters: 48250,
		AvgPower:       198.7,
		AvgSpeedMPS:    29.8 / 3.6,
	}

	got, changed := applyWeirdStatsDescription("", stats.StopStats{}, rideFact)
	want := "Longest uninterrupted segment: 48.3km - 199w - 29.8kmh #weirdstats"
	if got != want {
		t.Fatalf("unexpected description\nwant: %q\n got: %q", want, got)
	}
	if !changed {
		t.Fatalf("expected description to change")
	}
}

func TestApplyWeirdStatsDescription_ReplacesHashtagManagedLine(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             2,
		StopTotalSeconds:      42,
		TrafficLightStopCount: 1,
	}

	existing := "Morning ride\n\n3 stops (1m 35s total) · 2 at lights #weirdstats"
	want := "Morning ride\n\n2 stops (42s total) · 1 at lights #weirdstats"

	got, changed := applyWeirdStatsDescription(existing, snapshot, rideSegmentFact{})
	if got != want {
		t.Fatalf("unexpected description\nwant: %q\n got: %q", want, got)
	}
	if !changed {
		t.Fatalf("expected description to change")
	}
}

func TestBuildWeirdStatsLine(t *testing.T) {
	rideFact := rideSegmentFact{
		DistanceMeters: 48000,
		AvgPower:       200,
		AvgSpeedMPS:    30.0 / 3.6,
	}

	tests := []struct {
		name     string
		stats    stats.StopStats
		rideFact rideSegmentFact
		want     string
	}{
		{
			name:     "ride fact first with stops and lights",
			stats:    stats.StopStats{StopCount: 3, StopTotalSeconds: 95, TrafficLightStopCount: 2},
			rideFact: rideFact,
			want:     "Longest uninterrupted segment: 48km - 200w - 30kmh · 3 stops (1m 35s total) · 2 at lights",
		},
		{
			name:     "ride fact only",
			rideFact: rideSegmentFact{DistanceMeters: 48250, AvgPower: 198.7, AvgSpeedMPS: 29.8 / 3.6},
			want:     "Longest uninterrupted segment: 48.3km - 199w - 29.8kmh",
		},
		{
			name:  "stops only",
			stats: stats.StopStats{StopCount: 2, StopTotalSeconds: 42},
			want:  "2 stops (42s total)",
		},
		{
			name:  "lights only",
			stats: stats.StopStats{TrafficLightStopCount: 1},
			want:  "1 at lights",
		},
		{
			name: "empty stats",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWeirdStatsLine(tt.stats, tt.rideFact)
			if got != tt.want {
				t.Fatalf("unexpected line\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestBuildRideSegmentPart(t *testing.T) {
	tests := []struct {
		name string
		fact rideSegmentFact
		want string
	}{
		{
			name: "with power",
			fact: rideSegmentFact{DistanceMeters: 48000, AvgPower: 200, AvgSpeedMPS: 30.0 / 3.6},
			want: "Longest uninterrupted segment: 48km - 200w - 30kmh",
		},
		{
			name: "without power",
			fact: rideSegmentFact{DistanceMeters: 12345, AvgSpeedMPS: 25.0 / 3.6},
			want: "Longest uninterrupted segment: 12.3km - 25kmh",
		},
		{
			name: "missing speed",
			fact: rideSegmentFact{DistanceMeters: 12345},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRideSegmentPart(tt.fact)
			if got != tt.want {
				t.Fatalf("unexpected segment part\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestAppendWeirdstatsTag(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "appends tag",
			text: "2 stops (42s total)",
			want: "2 stops (42s total) #weirdstats",
		},
		{
			name: "dedupes trailing tag",
			text: "2 stops (42s total) #weirdstats",
			want: "2 stops (42s total) #weirdstats",
		},
		{
			name: "tag only",
			text: "   ",
			want: "#weirdstats",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendWeirdstatsTag(tt.text)
			if got != tt.want {
				t.Fatalf("unexpected tagged text\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestIsWeirdstatsManagedLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "legacy prefixed line",
			line: "Weirdstats: 2 stops (42s total)",
			want: true,
		},
		{
			name: "tag only line",
			line: "#weirdstats",
			want: true,
		},
		{
			name: "new stats line",
			line: "Longest uninterrupted segment: 48km - 200w - 30kmh · 2 stops (42s total) #weirdstats",
			want: true,
		},
		{
			name: "unrelated tagged line",
			line: "coffee with friends #weirdstats",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWeirdstatsManagedLine(tt.line)
			if got != tt.want {
				t.Fatalf("unexpected managed result: want %v got %v", tt.want, got)
			}
		})
	}
}

func TestLongestRideSegmentFact(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 0, Lon: 0, Time: start, Speed: 15, Power: 200},
		{Lat: 0.0045, Lon: 0, Time: start.Add(time.Minute), Speed: 15, Power: 200},
		{Lat: 0.0090, Lon: 0, Time: start.Add(2 * time.Minute), Speed: 0},
		{Lat: 0.0090, Lon: 0, Time: start.Add(3 * time.Minute), Speed: 0},
		{Lat: 0.0180, Lon: 0, Time: start.Add(4 * time.Minute), Speed: 15, Power: 250},
		{Lat: 0.0270, Lon: 0, Time: start.Add(5 * time.Minute), Speed: 15, Power: 250},
		{Lat: 0.0360, Lon: 0, Time: start.Add(6 * time.Minute), Speed: 15, Power: 250},
	}

	got := longestRideSegmentFact("Ride", points, gps.StopOptions{
		SpeedThreshold: 0.5,
		MinDuration:    30 * time.Second,
	})

	if got.DistanceMeters < 1900 || got.DistanceMeters > 2100 {
		t.Fatalf("expected longest segment around 2km, got %.1fm", got.DistanceMeters)
	}
	if got.AvgPower != 250 {
		t.Fatalf("expected 250W average power, got %.1f", got.AvgPower)
	}
	if got.AvgSpeedMPS != 15 {
		t.Fatalf("expected 15 m/s average speed, got %.2f", got.AvgSpeedMPS)
	}
}

func TestLongestRideSegmentFact_DoesNotSplitBriefSlowdown(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 0, Lon: 0, Time: start, Speed: 10, Power: 190},
		{Lat: 0.0045, Lon: 0, Time: start.Add(1 * time.Second), Speed: 10, Power: 190},
		{Lat: 0.0046, Lon: 0, Time: start.Add(2 * time.Second), Speed: 4, Power: 120},
		{Lat: 0.0047, Lon: 0, Time: start.Add(4 * time.Second), Speed: 4, Power: 120},
		{Lat: 0.0092, Lon: 0, Time: start.Add(6 * time.Second), Speed: 10, Power: 210},
		{Lat: 0.0137, Lon: 0, Time: start.Add(7 * time.Second), Speed: 10, Power: 210},
	}

	got := longestRideSegmentFact("Ride", points, gps.StopOptions{})

	if got.DistanceMeters < 1450 || got.DistanceMeters > 1550 {
		t.Fatalf("expected slowdown to remain inside segment, got %.1fm", got.DistanceMeters)
	}
	if got.AvgPower < 190 || got.AvgPower > 210 {
		t.Fatalf("expected average power to stay in-range, got %.1f", got.AvgPower)
	}
	if got.AvgSpeedMPS < 9.5 || got.AvgSpeedMPS > 10.5 {
		t.Fatalf("expected average speed near 10 m/s, got %.2f", got.AvgSpeedMPS)
	}
}

func TestBuildRideSegmentWindows(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		points []gps.Point
		want   []rideSegmentWindow
	}{
		{
			name: "splits on sustained slowdown",
			points: []gps.Point{
				{Time: start, Speed: 8},
				{Time: start.Add(4 * time.Second), Speed: 8},
				{Time: start.Add(10 * time.Second), Speed: 3},
				{Time: start.Add(16 * time.Second), Speed: 8},
				{Time: start.Add(20 * time.Second), Speed: 8},
			},
			want: []rideSegmentWindow{
				{start: start, end: start.Add(10 * time.Second)},
				{start: start.Add(16 * time.Second), end: start.Add(20 * time.Second)},
			},
		},
		{
			name: "keeps brief slowdown in one window",
			points: []gps.Point{
				{Time: start, Speed: 8},
				{Time: start.Add(4 * time.Second), Speed: 8},
				{Time: start.Add(10 * time.Second), Speed: 3},
				{Time: start.Add(13 * time.Second), Speed: 3},
				{Time: start.Add(14 * time.Second), Speed: 8},
				{Time: start.Add(20 * time.Second), Speed: 8},
			},
			want: []rideSegmentWindow{
				{start: start, end: start.Add(20 * time.Second)},
			},
		},
		{
			name: "drops sustained slow tail",
			points: []gps.Point{
				{Time: start, Speed: 8},
				{Time: start.Add(4 * time.Second), Speed: 8},
				{Time: start.Add(10 * time.Second), Speed: 3},
				{Time: start.Add(16 * time.Second), Speed: 3},
			},
			want: []rideSegmentWindow{
				{start: start, end: start.Add(10 * time.Second)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRideSegmentWindows(tt.points, longestRideSegmentMinSpeedMPS, longestRideSegmentMinSlowTime)
			if len(got) != len(tt.want) {
				t.Fatalf("unexpected window count: want %d got %d", len(tt.want), len(got))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("unexpected window %d: want %#v got %#v", i, tt.want[i], got[i])
				}
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
