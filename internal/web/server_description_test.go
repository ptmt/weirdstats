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
	rideFact := rideSegmentFact{
		DistanceMeters: 48000,
		AvgPower:       200,
		AvgSpeedMPS:    30.0 / 3.6,
	}
	coffeeFact := coffeeStopFact{Name: "Bean Machine"}
	routeFact := routeHighlightFact{Names: []string{"Victory Column", "Memorial Church"}}
	roadFact := roadCrossingFact{Count: 2, Roads: []string{"Unter den Linden", "Friedrichstrasse"}}
	line := "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column, Memorial Church · 2 road crossings: Unter den Linden, Friedrichstrasse · 3 stops (1m 35s total) · 2 at lights #weirdstats"

	tests := []struct {
		name       string
		existing   string
		stats      stats.StopStats
		rideFact   rideSegmentFact
		coffeeFact coffeeStopFact
		routeFact  routeHighlightFact
		roadFact   roadCrossingFact
		want       string
		changed    bool
	}{
		{
			name:       "appends to empty description",
			existing:   "",
			stats:      snapshot,
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			roadFact:   roadFact,
			want:       line,
			changed:    true,
		},
		{
			name:       "appends after existing text",
			existing:   "Morning ride with intervals",
			stats:      snapshot,
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			roadFact:   roadFact,
			want:       "Morning ride with intervals\n\n" + line,
			changed:    true,
		},
		{
			name:       "replaces previous weirdstats line and keeps paragraphs",
			existing:   "First paragraph.\n\nSecond paragraph.\nWeirdstats: 1 stops (12s total)",
			stats:      snapshot,
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			roadFact:   roadFact,
			want:       "First paragraph.\n\nSecond paragraph.\n\n" + line,
			changed:    true,
		},
		{
			name:       "no change when same line already present",
			existing:   "Morning ride with intervals\n\n" + line,
			stats:      snapshot,
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			roadFact:   roadFact,
			want:       "Morning ride with intervals\n\n" + line,
			changed:    false,
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
			got, changed := applyWeirdStatsDescription(tt.existing, tt.stats, tt.rideFact, tt.coffeeFact, tt.routeFact, tt.roadFact)
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

	got, changed := applyWeirdStatsDescription("", stats.StopStats{}, rideFact, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
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

	got, changed := applyWeirdStatsDescription(existing, snapshot, rideSegmentFact{}, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
	if got != want {
		t.Fatalf("unexpected description\nwant: %q\n got: %q", want, got)
	}
	if !changed {
		t.Fatalf("expected description to change")
	}
}

func TestApplyWeirdStatsDescription_RemovesManagedLineWhenNoFactsRemain(t *testing.T) {
	existing := "Morning ride\n\n2 stops (42s total) · 1 at lights #weirdstats"

	got, changed := applyWeirdStatsDescription(existing, stats.StopStats{}, rideSegmentFact{}, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
	if got != "Morning ride" {
		t.Fatalf("unexpected description\nwant: %q\n got: %q", "Morning ride", got)
	}
	if !changed {
		t.Fatalf("expected description to change")
	}
}

func TestFilterWeirdStatsSnapshot(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             3,
		StopTotalSeconds:      95,
		TrafficLightStopCount: 2,
	}

	got := filterWeirdStatsSnapshot(snapshot, map[string]bool{
		weirdStatsFactStopSummary:       false,
		weirdStatsFactTrafficLightStops: true,
	})
	if got.StopCount != 0 || got.StopTotalSeconds != 0 {
		t.Fatalf("expected stop summary to be cleared, got %+v", got)
	}
	if got.TrafficLightStopCount != 2 {
		t.Fatalf("expected traffic-light stops to remain, got %+v", got)
	}
}

func TestBuildWeirdStatsLine(t *testing.T) {
	rideFact := rideSegmentFact{
		DistanceMeters: 48000,
		AvgPower:       200,
		AvgSpeedMPS:    30.0 / 3.6,
	}
	coffeeFact := coffeeStopFact{Name: "Bean Machine"}
	routeFact := routeHighlightFact{Names: []string{"Victory Column", "Memorial Church"}}
	roadFact := roadCrossingFact{Count: 2, Roads: []string{"Unter den Linden", "Friedrichstrasse"}}

	tests := []struct {
		name       string
		stats      stats.StopStats
		rideFact   rideSegmentFact
		coffeeFact coffeeStopFact
		routeFact  routeHighlightFact
		roadFact   roadCrossingFact
		want       string
	}{
		{
			name:       "ride fact first with coffee, route highlights, stops and lights",
			stats:      stats.StopStats{StopCount: 3, StopTotalSeconds: 95, TrafficLightStopCount: 2},
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			roadFact:   roadFact,
			want:       "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column, Memorial Church · 2 road crossings: Unter den Linden, Friedrichstrasse · 3 stops (1m 35s total) · 2 at lights",
		},
		{
			name:     "ride fact only",
			rideFact: rideSegmentFact{DistanceMeters: 48250, AvgPower: 198.7, AvgSpeedMPS: 29.8 / 3.6},
			want:     "Longest uninterrupted segment: 48.3km - 199w - 29.8kmh",
		},
		{
			name:       "coffee fact only",
			coffeeFact: coffeeFact,
			want:       "Detected Coffee Stop: Bean Machine",
		},
		{
			name:      "route highlights only",
			routeFact: routeFact,
			want:      "Route highlights: Victory Column, Memorial Church",
		},
		{
			name:     "road crossings only",
			roadFact: roadFact,
			want:     "2 road crossings: Unter den Linden, Friedrichstrasse",
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
			got := buildWeirdStatsLine(tt.stats, tt.rideFact, tt.coffeeFact, tt.routeFact, tt.roadFact)
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

func TestBuildCoffeeStopPart(t *testing.T) {
	tests := []struct {
		name string
		fact coffeeStopFact
		want string
	}{
		{
			name: "named stop",
			fact: coffeeStopFact{Name: "Bean Machine"},
			want: "Detected Coffee Stop: Bean Machine",
		},
		{
			name: "missing name",
			fact: coffeeStopFact{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCoffeeStopPart(tt.fact)
			if got != tt.want {
				t.Fatalf("unexpected coffee part\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestBuildRouteHighlightPart(t *testing.T) {
	tests := []struct {
		name string
		fact routeHighlightFact
		want string
	}{
		{
			name: "named highlights",
			fact: routeHighlightFact{Names: []string{"Victory Column", "Memorial Church"}},
			want: "Route highlights: Victory Column, Memorial Church",
		},
		{
			name: "dedupes and trims",
			fact: routeHighlightFact{Names: []string{" Victory Column ", "victory   column", "Memorial Church"}},
			want: "Route highlights: Victory Column, Memorial Church",
		},
		{
			name: "missing highlights",
			fact: routeHighlightFact{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRouteHighlightPart(tt.fact)
			if got != tt.want {
				t.Fatalf("unexpected route part\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestBuildRoadCrossingPart(t *testing.T) {
	tests := []struct {
		name string
		fact roadCrossingFact
		want string
	}{
		{
			name: "named crossings",
			fact: roadCrossingFact{Count: 2, Roads: []string{"Unter den Linden", "Friedrichstrasse"}},
			want: "2 road crossings: Unter den Linden, Friedrichstrasse",
		},
		{
			name: "single named crossing",
			fact: roadCrossingFact{Count: 1, Roads: []string{"Unter den Linden"}},
			want: "Road crossing: Unter den Linden",
		},
		{
			name: "count only",
			fact: roadCrossingFact{Count: 2},
			want: "2 road crossings",
		},
		{
			name: "missing crossings",
			fact: roadCrossingFact{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRoadCrossingPart(tt.fact)
			if got != tt.want {
				t.Fatalf("unexpected road crossing part\nwant: %q\n got: %q", tt.want, got)
			}
		})
	}
}

func TestBuildRoadCrossingFact(t *testing.T) {
	stops := []storage.ActivityStop{
		{Lat: 52.5200, Lon: 13.4050, HasRoadCrossing: true, CrossingRoad: " Unter den Linden "},
		{HasRoadCrossing: false, CrossingRoad: "Ignored"},
		{Lat: 52.5201, Lon: 13.4051, HasRoadCrossing: true, CrossingRoad: "unter   den linden"},
		{Lat: 52.5202, Lon: 13.4052, HasRoadCrossing: true, CrossingRoad: "Friedrichstrasse"},
	}

	got := buildRoadCrossingFact(stops)
	if got.Count != 3 {
		t.Fatalf("expected 3 road crossings, got %+v", got)
	}
	if len(got.Roads) != 2 {
		t.Fatalf("expected 2 unique road names, got %+v", got)
	}
	if got.Roads[0] != "Unter den Linden" || got.Roads[1] != "Friedrichstrasse" {
		t.Fatalf("unexpected road names: %+v", got.Roads)
	}
	if len(got.Locations) != 3 {
		t.Fatalf("expected 3 crossing locations, got %+v", got.Locations)
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

func TestSplitStoredActivityDescription(t *testing.T) {
	tests := []struct {
		name        string
		description string
		wantText    string
		wantCount   int
	}{
		{
			name:        "keeps strava text and counts managed facts",
			description: "Met up with Sam at the cafe.\n\n2 stops (42s total) · 1 at lights #weirdstats",
			wantText:    "Met up with Sam at the cafe.",
			wantCount:   2,
		},
		{
			name:        "counts legacy managed line",
			description: "Weirdstats: 2 stops (42s total)",
			wantText:    "",
			wantCount:   1,
		},
		{
			name:        "ignores normal descriptions",
			description: "Just a normal description",
			wantText:    "Just a normal description",
			wantCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotCount := splitStoredActivityDescription(tt.description)
			if gotText != tt.wantText {
				t.Fatalf("unexpected description text\nwant: %q\n got: %q", tt.wantText, gotText)
			}
			if gotCount != tt.wantCount {
				t.Fatalf("unexpected detected fact count: want %d got %d", tt.wantCount, gotCount)
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
			line: "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column · 2 road crossings: Unter den Linden, Friedrichstrasse · 2 stops (42s total) #weirdstats",
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
