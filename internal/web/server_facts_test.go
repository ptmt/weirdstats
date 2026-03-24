package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
)

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
	if got.StartIndex != 4 || got.EndIndex != 6 {
		t.Fatalf("expected segment indices 4..6, got %+v", got)
	}
	if got.StartLat != points[4].Lat || got.EndLat != points[6].Lat {
		t.Fatalf("expected segment endpoints to match route points, got %+v", got)
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
		{Time: start.Add(9*time.Minute + 30*time.Second), Speed: 0},
		{Time: start.Add(10 * time.Minute), Speed: 7},
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
	if !got.HasLocation {
		t.Fatalf("expected coffee stop location, got %+v", got)
	}
	if got.Lat < 52.5202 || got.Lat > 52.5204 || got.Lon < 13.4049 || got.Lon > 13.4051 {
		t.Fatalf("expected coffee stop coordinates near cafe, got %+v", got)
	}
}

func TestDetectCoffeeStopFact_IgnoresShortPause(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4047, Time: start, Speed: 7},
		{Lat: 52.5202, Lon: 13.4049, Time: start.Add(1 * time.Minute), Speed: 7},
		{Lat: 52.5203, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 0},
		{Lat: 52.52031, Lon: 13.40501, Time: start.Add(4*time.Minute + 59*time.Second), Speed: 0},
		{Lat: 52.5206, Lon: 13.4053, Time: start.Add(5 * time.Minute), Speed: 8},
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
	if got[0].lat == 0 || got[0].lon == 0 {
		t.Fatalf("expected highlight candidate coordinates, got %+v", got[0])
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
	if len(got.Locations) != 2 {
		t.Fatalf("expected 2 route highlight locations, got %+v", got)
	}
	if got.Locations[0].Lat == 0 || got.Locations[0].Lon == 0 {
		t.Fatalf("expected route highlight coordinates, got %+v", got.Locations[0])
	}
}

func TestBuildActivityMapFacts(t *testing.T) {
	start := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 9},
		{Lat: 52.5201, Lon: 13.4045, Time: start.Add(1 * time.Minute), Speed: 9},
		{Lat: 52.5202, Lon: 13.4050, Time: start.Add(2 * time.Minute), Speed: 9},
	}
	stopViews := []StopView{
		{Lat: 52.5201, Lon: 13.4045, Duration: "45s"},
		{Lat: 52.5202, Lon: 13.4050, Duration: "30s", HasTrafficLight: true},
	}
	rideFact := rideSegmentFact{
		DistanceMeters: 1200,
		AvgPower:       210,
		AvgSpeedMPS:    10,
		StartIndex:     0,
		EndIndex:       2,
		StartLat:       points[0].Lat,
		StartLon:       points[0].Lon,
		EndLat:         points[2].Lat,
		EndLon:         points[2].Lon,
	}
	coffeeFact := coffeeStopFact{Name: "Bean Machine", Lat: 52.52025, Lon: 13.40505, HasLocation: true}
	routeFact := routeHighlightFact{
		Names: []string{"Victory Column"},
		Locations: []routeHighlightLocation{
			{Name: "Victory Column", Lat: 52.5203, Lon: 13.4051},
		},
	}
	roadFact := roadCrossingFact{
		Count: 1,
		Roads: []string{"Unter den Linden"},
		Locations: []roadCrossingLocation{
			{Lat: 52.5202, Lon: 13.4050, Road: "Unter den Linden"},
		},
	}

	got := buildActivityMapFacts(stopViews, points, rideFact, coffeeFact, routeFact, roadFact)
	if len(got) != 6 {
		t.Fatalf("expected 6 map facts, got %+v", got)
	}
	if got[0].ID != weirdStatsFactLongestSegment || len(got[0].Path) != 3 {
		t.Fatalf("expected longest segment fact with route path, got %+v", got[0])
	}
	if got[1].ID != weirdStatsFactCoffeeStop || len(got[1].Points) != 1 {
		t.Fatalf("expected coffee stop point fact, got %+v", got[1])
	}
	if got[4].ID != weirdStatsFactStopSummary || len(got[4].Points) != 2 {
		t.Fatalf("expected stop summary to include both stop points, got %+v", got[4])
	}
	if got[5].ID != weirdStatsFactTrafficLightStops || len(got[5].Points) != 1 {
		t.Fatalf("expected traffic-light fact to include matching stop points, got %+v", got[5])
	}
}
