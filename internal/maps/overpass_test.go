package maps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOverpassClient_RequestsAndParses(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("data") == "" {
			t.Fatalf("expected data query param")
		}
		atomic.AddInt32(&requestCount, 1)
		resp := overpassResponse{
			Elements: []overpassElement{
				{Lat: 40.0, Lon: -73.0, Tags: map[string]string{"highway": "traffic_signals", "name": "Main"}},
				{Type: "way", Center: &overpassLatLon{Lat: 40.1, Lon: -73.1}, Tags: map[string]string{"amenity": "cafe", "name": "Cafe XYZ"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	features, err := client.NearbyFeatures(40.0, -73.0)
	if err != nil {
		t.Fatalf("NearbyFeatures error: %v", err)
	}
	if len(features) != 1 || features[0].Type != FeatureTrafficLight {
		t.Fatalf("unexpected features: %+v", features)
	}

	ctx := context.Background()
	pois, err := client.FetchPOIs(ctx, BBox{South: 1, West: 1, North: 2, East: 2}, true, true)
	if err != nil {
		t.Fatalf("FetchPOIs error: %v", err)
	}
	if len(pois) != 2 {
		t.Fatalf("expected 2 pois, got %d", len(pois))
	}
	if pois[0].Type != FeatureTrafficLight || pois[1].Type != FeatureCafe {
		t.Fatalf("unexpected poi types: %+v", pois)
	}
	if pois[1].Lat != 40.1 || pois[1].Lon != -73.1 {
		t.Fatalf("expected center coordinates for cafe, got %+v", pois[1])
	}

	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("expected 2 requests (no cache), got %d", got)
	}
}

func TestOverpassClient_FetchNearbyFoodPOIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("data")
		if query == "" || !strings.Contains(query, `around:45`) || !strings.Contains(query, `"amenity"~"^(cafe|restaurant)$"`) {
			t.Fatalf("unexpected nearby food query: %q", query)
		}

		resp := overpassResponse{
			Elements: []overpassElement{
				{Type: "node", Lat: 40.0, Lon: -73.0, Tags: map[string]string{"amenity": "restaurant", "name": "Lunch Spot"}},
				{Type: "way", Center: &overpassLatLon{Lat: 40.0002, Lon: -73.0001}, Tags: map[string]string{"amenity": "cafe", "name": "Bean House"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	pois, err := client.FetchNearbyFoodPOIs(context.Background(), 40.0, -73.0, 45)
	if err != nil {
		t.Fatalf("FetchNearbyFoodPOIs error: %v", err)
	}
	if len(pois) != 2 {
		t.Fatalf("expected 2 pois, got %d", len(pois))
	}
	if pois[0].Type != FeatureRestaurant || pois[1].Type != FeatureCafe {
		t.Fatalf("unexpected poi types: %+v", pois)
	}
	if pois[1].Lat != 40.0002 || pois[1].Lon != -73.0001 {
		t.Fatalf("expected center coordinates for cafe, got %+v", pois[1])
	}
}

func TestOverpassClient_FetchLandmarkPOIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("data")
		if query == "" ||
			!strings.Contains(query, `["tourism"~"^(attraction|artwork|museum|viewpoint)$"]`) ||
			!strings.Contains(query, `["historic"~"^(monument|memorial|castle|ruins|archaeological_site)$"]`) ||
			!strings.Contains(query, `["amenity"="place_of_worship"]`) ||
			!strings.Contains(query, `["building"~"^(church|cathedral)$"]`) ||
			!strings.Contains(query, `["name"]`) {
			t.Fatalf("unexpected landmark query: %q", query)
		}

		resp := overpassResponse{
			Elements: []overpassElement{
				{Type: "node", Lat: 40.0, Lon: -73.0, Tags: map[string]string{"tourism": "attraction", "name": "Grand Arch", "wikipedia": "en:Grand Arch"}},
				{Type: "way", Center: &overpassLatLon{Lat: 40.0003, Lon: -73.0002}, Tags: map[string]string{"building": "church", "name": "Old Church"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	pois, err := client.FetchLandmarkPOIs(context.Background(), BBox{South: 1, West: 1, North: 2, East: 2})
	if err != nil {
		t.Fatalf("FetchLandmarkPOIs error: %v", err)
	}
	if len(pois) != 2 {
		t.Fatalf("expected 2 pois, got %d", len(pois))
	}
	if pois[0].Type != FeatureType("attraction") || pois[1].Type != FeatureType("church") {
		t.Fatalf("unexpected poi types: %+v", pois)
	}
	if pois[1].Lat != 40.0003 || pois[1].Lon != -73.0002 {
		t.Fatalf("expected center coordinates for church, got %+v", pois[1])
	}
}

func TestOverpassClient_FetchNearbyRoads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := overpassResponse{
			Elements: []overpassElement{
				{
					Type: "way",
					ID:   12345,
					Tags: map[string]string{"highway": "residential", "name": "Main Street"},
					Geometry: []overpassLatLon{
						{Lat: 40.0, Lon: -73.0},
						{Lat: 40.001, Lon: -73.001},
						{Lat: 40.002, Lon: -73.002},
					},
				},
				{
					Type: "way",
					ID:   67890,
					Tags: map[string]string{"highway": "secondary"},
					Geometry: []overpassLatLon{
						{Lat: 40.0, Lon: -73.005},
						{Lat: 40.001, Lon: -73.006},
					},
				},
				// Should be filtered out - not a "way"
				{
					Type: "node",
					ID:   11111,
					Lat:  40.0,
					Lon:  -73.0,
					Tags: map[string]string{"highway": "traffic_signals"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	roads, err := client.FetchNearbyRoads(context.Background(), 40.0, -73.0, 30)
	if err != nil {
		t.Fatalf("FetchNearbyRoads error: %v", err)
	}
	if len(roads) != 2 {
		t.Fatalf("expected 2 roads, got %d", len(roads))
	}
	if roads[0].Name != "Main Street" || roads[0].Highway != "residential" {
		t.Fatalf("unexpected first road: %+v", roads[0])
	}
	if len(roads[0].Geometry) != 3 {
		t.Fatalf("expected 3 points in first road geometry, got %d", len(roads[0].Geometry))
	}
	if roads[1].Highway != "secondary" {
		t.Fatalf("unexpected second road: %+v", roads[1])
	}
}

func TestOverpassClient_FetchMapContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("data")
		for _, want := range []string{
			`["highway"~"^(motorway|trunk|primary|secondary|tertiary|motorway_link|trunk_link|primary_link|secondary_link|tertiary_link)$"]`,
			`["waterway"~"^(river|canal|stream)$"]`,
			`["natural"="water"]`,
			`["waterway"="riverbank"]`,
			`["landuse"="reservoir"]`,
			`["natural"~"^(peak|volcano)$"]["name"]`,
		} {
			if !strings.Contains(query, want) {
				t.Fatalf("unexpected map context query, missing %q in %q", want, query)
			}
		}

		resp := overpassResponse{
			Elements: []overpassElement{
				{
					Type: "way",
					ID:   1001,
					Tags: map[string]string{"highway": "primary", "name": "A1"},
					Geometry: []overpassLatLon{
						{Lat: 40.0, Lon: -73.0},
						{Lat: 40.002, Lon: -73.002},
					},
				},
				{
					Type: "way",
					ID:   1002,
					Tags: map[string]string{"waterway": "river", "name": "Blue River"},
					Geometry: []overpassLatLon{
						{Lat: 40.0, Lon: -73.01},
						{Lat: 40.003, Lon: -73.011},
					},
				},
				{
					Type: "way",
					ID:   1003,
					Tags: map[string]string{"natural": "water", "name": "Silver Lake"},
					Geometry: []overpassLatLon{
						{Lat: 40.01, Lon: -73.01},
						{Lat: 40.011, Lon: -73.012},
						{Lat: 40.013, Lon: -73.011},
					},
				},
				{
					Type: "node",
					ID:   1004,
					Lat:  40.02,
					Lon:  -73.02,
					Tags: map[string]string{"natural": "peak", "name": "Mount Example"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	ctx, err := client.FetchMapContext(context.Background(), BBox{South: 1, West: 1, North: 2, East: 2})
	if err != nil {
		t.Fatalf("FetchMapContext error: %v", err)
	}
	if len(ctx.Roads) != 1 {
		t.Fatalf("expected 1 road, got %d", len(ctx.Roads))
	}
	if len(ctx.Waterways) != 1 {
		t.Fatalf("expected 1 waterway, got %d", len(ctx.Waterways))
	}
	if len(ctx.Waters) != 1 {
		t.Fatalf("expected 1 water area, got %d", len(ctx.Waters))
	}
	if len(ctx.Peaks) != 1 {
		t.Fatalf("expected 1 peak, got %d", len(ctx.Peaks))
	}
	if ctx.Roads[0].Name != "A1" || ctx.Roads[0].Highway != "primary" {
		t.Fatalf("unexpected roads: %+v", ctx.Roads)
	}
	if ctx.Waterways[0].Name != "Blue River" || ctx.Waterways[0].Kind != "river" {
		t.Fatalf("unexpected waterways: %+v", ctx.Waterways)
	}
	if ctx.Waters[0].Name != "Silver Lake" || ctx.Waters[0].Kind != "water" {
		t.Fatalf("unexpected waters: %+v", ctx.Waters)
	}
	if ctx.Peaks[0].Name != "Mount Example" || ctx.Peaks[0].Type != FeatureType("peak") {
		t.Fatalf("unexpected peaks: %+v", ctx.Peaks)
	}
}

func TestOverpassClient_RoundRobinMirrors(t *testing.T) {
	var firstHits, secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`overpass down`))
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		resp := overpassResponse{
			Elements: []overpassElement{{Lat: 1, Lon: 2, Tags: map[string]string{"highway": "traffic_signals"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer second.Close()

	client := &OverpassClient{
		MirrorURLs:   []string{first.URL, second.URL},
		HTTPClient:   first.Client(),
		MaxAttempts:  2,
		DisableCache: true,
	}

	features, err := client.NearbyFeatures(0, 0)
	if err != nil {
		t.Fatalf("round robin failed: %v", err)
	}
	if len(features) != 1 || features[0].Type != FeatureTrafficLight {
		t.Fatalf("unexpected features: %+v", features)
	}
	if atomic.LoadInt32(&firstHits) != 1 || atomic.LoadInt32(&secondHits) != 1 {
		t.Fatalf("expected 1 hit per mirror, got first=%d second=%d", firstHits, secondHits)
	}
}
