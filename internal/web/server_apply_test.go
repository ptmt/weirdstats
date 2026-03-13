package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
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
	line := "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column, Memorial Church · 3 stops (1m 35s total) · 2 at lights #weirdstats"

	tests := []struct {
		name       string
		existing   string
		stats      stats.StopStats
		rideFact   rideSegmentFact
		coffeeFact coffeeStopFact
		routeFact  routeHighlightFact
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
			got, changed := applyWeirdStatsDescription(tt.existing, tt.stats, tt.rideFact, tt.coffeeFact, tt.routeFact)
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

	got, changed := applyWeirdStatsDescription("", stats.StopStats{}, rideFact, coffeeStopFact{}, routeHighlightFact{})
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

	got, changed := applyWeirdStatsDescription(existing, snapshot, rideSegmentFact{}, coffeeStopFact{}, routeHighlightFact{})
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
	coffeeFact := coffeeStopFact{Name: "Bean Machine"}
	routeFact := routeHighlightFact{Names: []string{"Victory Column", "Memorial Church"}}

	tests := []struct {
		name       string
		stats      stats.StopStats
		rideFact   rideSegmentFact
		coffeeFact coffeeStopFact
		routeFact  routeHighlightFact
		want       string
	}{
		{
			name:       "ride fact first with coffee, route highlights, stops and lights",
			stats:      stats.StopStats{StopCount: 3, StopTotalSeconds: 95, TrafficLightStopCount: 2},
			rideFact:   rideFact,
			coffeeFact: coffeeFact,
			routeFact:  routeFact,
			want:       "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column, Memorial Church · 3 stops (1m 35s total) · 2 at lights",
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
			got := buildWeirdStatsLine(tt.stats, tt.rideFact, tt.coffeeFact, tt.routeFact)
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
			line: "Longest uninterrupted segment: 48km - 200w - 30kmh · Detected Coffee Stop: Bean Machine · Route highlights: Victory Column · 2 stops (42s total) #weirdstats",
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

func TestBuildPauseWindows(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Time: start, Speed: 6},
		{Time: start.Add(30 * time.Second), Speed: 6},
		{Time: start.Add(1 * time.Minute), Speed: 0},
		{Time: start.Add(3 * time.Minute), Speed: 0},
		{Time: start.Add(6 * time.Minute), Speed: 0},
		{Time: start.Add(7 * time.Minute), Speed: 7},
		{Time: start.Add(8 * time.Minute), Speed: 0},
		{Time: start.Add(12 * time.Minute), Speed: 0},
		{Time: start.Add(13 * time.Minute), Speed: 7},
	}

	got := buildPauseWindows(points, coffeeStopSpeedThresholdMPS, coffeeStopMinDuration)
	if len(got) != 1 {
		t.Fatalf("expected 1 qualifying pause, got %d", len(got))
	}
	if got[0].start != start.Add(1*time.Minute) || got[0].end != start.Add(6*time.Minute) {
		t.Fatalf("unexpected pause window: %#v", got[0])
	}
	if got[0].duration != 5*time.Minute {
		t.Fatalf("unexpected pause duration: %s", got[0].duration)
	}
}

func TestDetectCoffeeStopFact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"elements": []map[string]any{
				{
					"type": "node",
					"lat":  52.52035,
					"lon":  13.40505,
					"tags": map[string]any{"amenity": "restaurant", "name": "Lunch Spot"},
				},
				{
					"type":   "way",
					"center": map[string]any{"lat": 52.52031, "lon": 13.40501},
					"tags":   map[string]any{"amenity": "cafe", "name": "Bean Machine"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &maps.OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4047, Time: start, Speed: 7},
		{Lat: 52.5202, Lon: 13.4049, Time: start.Add(1 * time.Minute), Speed: 7},
		{Lat: 52.5203, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 0},
		{Lat: 52.52031, Lon: 13.40501, Time: start.Add(5 * time.Minute), Speed: 0},
		{Lat: 52.52032, Lon: 13.40502, Time: start.Add(7 * time.Minute), Speed: 0},
		{Lat: 52.5206, Lon: 13.4053, Time: start.Add(8 * time.Minute), Speed: 8},
	}

	got, err := detectCoffeeStopFact(context.Background(), "Ride", points, client)
	if err != nil {
		t.Fatalf("detectCoffeeStopFact error: %v", err)
	}
	if got.Name != "Bean Machine" {
		t.Fatalf("expected Bean Machine, got %+v", got)
	}
}

func TestDetectCoffeeStopFact_IgnoresShortPause(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4047, Time: start, Speed: 7},
		{Lat: 52.5202, Lon: 13.4049, Time: start.Add(1 * time.Minute), Speed: 7},
		{Lat: 52.5203, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 0},
		{Lat: 52.52031, Lon: 13.40501, Time: start.Add(6*time.Minute + 59*time.Second), Speed: 0},
		{Lat: 52.5206, Lon: 13.4053, Time: start.Add(7 * time.Minute), Speed: 8},
	}

	got, err := detectCoffeeStopFact(context.Background(), "Ride", points, &maps.OverpassClient{
		BaseURL:      "http://127.0.0.1:1",
		HTTPClient:   &http.Client{Timeout: 100 * time.Millisecond},
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("detectCoffeeStopFact error: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("expected no coffee stop, got %+v", got)
	}
}

func TestDetectCoffeeStopFact_RequiresMovement(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4047, Time: start, Speed: 0},
		{Lat: 52.5202, Lon: 13.4049, Time: start.Add(6 * time.Minute), Speed: 0},
	}

	got, err := detectCoffeeStopFact(context.Background(), "Ride", points, &maps.OverpassClient{
		BaseURL:      "http://127.0.0.1:1",
		HTTPClient:   &http.Client{Timeout: 100 * time.Millisecond},
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("detectCoffeeStopFact error: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("expected no coffee stop, got %+v", got)
	}
}

func TestMinDistanceToRouteMeters(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start},
		{Lat: 52.5200, Lon: 13.4080, Time: start.Add(1 * time.Minute)},
	}

	onRoute := minDistanceToRouteMeters(52.5200, 13.4060, points)
	if onRoute > 1 {
		t.Fatalf("expected on-route point to be near zero, got %.2fm", onRoute)
	}

	nearRoute := minDistanceToRouteMeters(52.52135, 13.4060, points)
	if nearRoute < 140 || nearRoute > 160 {
		t.Fatalf("expected point about 150m from route, got %.2fm", nearRoute)
	}
}

func TestBuildRouteHighlightCandidates(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start},
		{Lat: 52.5200, Lon: 13.4080, Time: start.Add(1 * time.Minute)},
	}

	pois := []maps.POI{
		{
			Feature: maps.Feature{Name: "Brandenburg Gate"},
			Lat:     52.5201,
			Lon:     13.4055,
			Tags: map[string]string{
				"tourism":   "attraction",
				"wikidata":  "Q82494",
				"wikipedia": "en:Brandenburg Gate",
			},
		},
		{
			Feature: maps.Feature{Name: "Neighborhood Church"},
			Lat:     52.5206,
			Lon:     13.4062,
			Tags: map[string]string{
				"building": "church",
			},
		},
		{
			Feature: maps.Feature{Name: "Far Museum"},
			Lat:     52.5230,
			Lon:     13.4060,
			Tags: map[string]string{
				"tourism":   "museum",
				"wikidata":  "Q1",
				"wikipedia": "en:Far Museum",
			},
		},
		{
			Feature: maps.Feature{Name: "Brandenburg Gate"},
			Lat:     52.5202,
			Lon:     13.4056,
			Tags: map[string]string{
				"tourism": "attraction",
			},
		},
	}

	got := buildRouteHighlightCandidates(points, pois, routeHighlightMaxDistanceM)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	if got[0].name != "Brandenburg Gate" {
		t.Fatalf("expected Brandenburg Gate first, got %+v", got)
	}
	if got[1].name != "Neighborhood Church" {
		t.Fatalf("expected Neighborhood Church second, got %+v", got)
	}
}

func TestDetectRouteHighlightFact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"elements": []map[string]any{
				{
					"type": "node",
					"lat":  52.5201,
					"lon":  13.4055,
					"tags": map[string]any{
						"name":      "Brandenburg Gate",
						"tourism":   "attraction",
						"wikidata":  "Q82494",
						"wikipedia": "en:Brandenburg Gate",
					},
				},
				{
					"type": "node",
					"lat":  52.5206,
					"lon":  13.4062,
					"tags": map[string]any{
						"name":     "Neighborhood Church",
						"building": "church",
					},
				},
				{
					"type": "node",
					"lat":  52.5230,
					"lon":  13.4060,
					"tags": map[string]any{
						"name":      "Far Museum",
						"tourism":   "museum",
						"wikidata":  "Q1",
						"wikipedia": "en:Far Museum",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &maps.OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start},
		{Lat: 52.5200, Lon: 13.4080, Time: start.Add(1 * time.Minute)},
	}

	got, err := detectRouteHighlightFact(context.Background(), points, client)
	if err != nil {
		t.Fatalf("detectRouteHighlightFact error: %v", err)
	}
	if len(got.Names) != 2 {
		t.Fatalf("expected 2 route highlights, got %+v", got)
	}
	if got.Names[0] != "Brandenburg Gate" || got.Names[1] != "Neighborhood Church" {
		t.Fatalf("unexpected route highlights: %+v", got)
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
