package gps

import (
	"testing"
	"time"

	"weirdstats/internal/maps"
)

func TestDetectRoadCrossing_CrossesRoad(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	// Path that crosses a road running west-east
	points := []Point{
		{Lat: 40.0000, Lon: -73.0001, Time: base, Speed: 0},                            // stop
		{Lat: 40.0000, Lon: -73.0001, Time: base.Add(5 * time.Second), Speed: 0},       // still stopped
		{Lat: 40.0001, Lon: -73.0001, Time: base.Add(10 * time.Second), Speed: 2},      // start moving north
		{Lat: 40.0003, Lon: -73.0001, Time: base.Add(15 * time.Second), Speed: 2},      // crossing road
		{Lat: 40.0005, Lon: -73.0001, Time: base.Add(20 * time.Second), Speed: 2},      // past road
	}

	// Road running west-east at lat 40.0002
	roads := []maps.Road{
		{
			ID:      1,
			Name:    "Main Street",
			Highway: "residential",
			Geometry: []maps.LatLon{
				{Lat: 40.0002, Lon: -73.001},
				{Lat: 40.0002, Lon: -73.000},
			},
		},
	}

	result := DetectRoadCrossing(points, 2, roads) // start checking from point 2 (after stop)
	if !result.Crossed {
		t.Fatal("expected crossing to be detected")
	}
	if result.RoadName != "Main Street" {
		t.Fatalf("expected road name 'Main Street', got '%s'", result.RoadName)
	}
	if result.RoadType != "residential" {
		t.Fatalf("expected road type 'residential', got '%s'", result.RoadType)
	}
}

func TestDetectRoadCrossing_NoCrossing(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	// Path that goes parallel to the road (doesn't cross)
	points := []Point{
		{Lat: 40.0000, Lon: -73.0001, Time: base, Speed: 0},
		{Lat: 40.0000, Lon: -73.0001, Time: base.Add(5 * time.Second), Speed: 0},
		{Lat: 40.0000, Lon: -73.0002, Time: base.Add(10 * time.Second), Speed: 2}, // moving east
		{Lat: 40.0000, Lon: -73.0003, Time: base.Add(15 * time.Second), Speed: 2},
		{Lat: 40.0000, Lon: -73.0004, Time: base.Add(20 * time.Second), Speed: 2},
	}

	// Road running west-east but north of the path
	roads := []maps.Road{
		{
			ID:      1,
			Name:    "Main Street",
			Highway: "residential",
			Geometry: []maps.LatLon{
				{Lat: 40.0002, Lon: -73.001},
				{Lat: 40.0002, Lon: -73.000},
			},
		},
	}

	result := DetectRoadCrossing(points, 2, roads)
	if result.Crossed {
		t.Fatal("expected no crossing to be detected")
	}
}

func TestDetectRoadCrossing_NoRoads(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	points := []Point{
		{Lat: 40.0, Lon: -73.0, Time: base, Speed: 0},
		{Lat: 40.1, Lon: -73.0, Time: base.Add(5 * time.Second), Speed: 2},
	}

	result := DetectRoadCrossing(points, 0, nil)
	if result.Crossed {
		t.Fatal("expected no crossing with empty roads")
	}

	result = DetectRoadCrossing(points, 0, []maps.Road{})
	if result.Crossed {
		t.Fatal("expected no crossing with empty roads slice")
	}
}

func TestDetectRoadCrossing_InvalidIndex(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	points := []Point{
		{Lat: 40.0, Lon: -73.0, Time: base, Speed: 0},
		{Lat: 40.1, Lon: -73.0, Time: base.Add(5 * time.Second), Speed: 2},
	}
	roads := []maps.Road{{ID: 1, Geometry: []maps.LatLon{{Lat: 40.0, Lon: -73.0}, {Lat: 40.1, Lon: -73.0}}}}

	// Test with invalid indices
	result := DetectRoadCrossing(points, -1, roads)
	if result.Crossed {
		t.Fatal("expected no crossing with negative index")
	}

	result = DetectRoadCrossing(points, 10, roads)
	if result.Crossed {
		t.Fatal("expected no crossing with out-of-bounds index")
	}

	result = DetectRoadCrossing(points, 1, roads) // last point - no next point
	if result.Crossed {
		t.Fatal("expected no crossing when starting at last point")
	}
}

func TestFindStopEndIndex(t *testing.T) {
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	points := []Point{
		{Lat: 1, Lon: 1, Time: base, Speed: 5},
		{Lat: 1, Lon: 1, Time: base.Add(10 * time.Second), Speed: 0.3},  // below threshold
		{Lat: 1, Lon: 1, Time: base.Add(20 * time.Second), Speed: 0.2},  // still below
		{Lat: 1, Lon: 1, Time: base.Add(30 * time.Second), Speed: 0.1},  // still below
		{Lat: 1, Lon: 1, Time: base.Add(40 * time.Second), Speed: 2.0},  // above threshold - stop ends
		{Lat: 2, Lon: 2, Time: base.Add(50 * time.Second), Speed: 3.0},
	}

	// Stop starts at 10 seconds (point 1)
	idx := FindStopEndIndex(points, 10.0, 0.5, 0)
	if idx != 4 {
		t.Fatalf("expected stop end index 4, got %d", idx)
	}
}

func TestSegmentsIntersect(t *testing.T) {
	tests := []struct {
		name     string
		a, b     segment
		expected bool
	}{
		{
			name:     "crossing segments",
			a:        segment{x1: 0, y1: 0, x2: 2, y2: 2},
			b:        segment{x1: 0, y1: 2, x2: 2, y2: 0},
			expected: true,
		},
		{
			name:     "parallel segments",
			a:        segment{x1: 0, y1: 0, x2: 2, y2: 0},
			b:        segment{x1: 0, y1: 1, x2: 2, y2: 1},
			expected: false,
		},
		{
			name:     "non-intersecting",
			a:        segment{x1: 0, y1: 0, x2: 1, y2: 0},
			b:        segment{x1: 2, y1: 0, x2: 3, y2: 0},
			expected: false,
		},
		{
			name:     "touching at endpoint",
			a:        segment{x1: 0, y1: 0, x2: 1, y2: 1},
			b:        segment{x1: 1, y1: 1, x2: 2, y2: 0},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := segmentsIntersect(tt.a, tt.b)
			if result != tt.expected {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestHaversineMeters(t *testing.T) {
	// Test with known distance: ~111km per degree of latitude at equator
	dist := haversineMeters(0, 0, 1, 0)
	if dist < 110000 || dist > 112000 {
		t.Fatalf("expected ~111km, got %.0f meters", dist)
	}

	// Test same point
	dist = haversineMeters(40.0, -73.0, 40.0, -73.0)
	if dist != 0 {
		t.Fatalf("expected 0 for same point, got %f", dist)
	}
}
