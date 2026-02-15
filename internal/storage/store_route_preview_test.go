package storage

import "testing"

func TestSampleActivityRoutePointsKeepsEndpoints(t *testing.T) {
	points := make([]ActivityRoutePoint, 0, 11)
	for i := 0; i < 11; i++ {
		points = append(points, ActivityRoutePoint{Lat: 40 + (float64(i) * 0.001), Lon: -73 + (float64(i) * 0.001)})
	}

	got := sampleActivityRoutePoints(points, 4)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 points, got %d", len(got))
	}
	if len(got) > 5 {
		t.Fatalf("expected sampled points to stay close to max, got %d", len(got))
	}
	if got[0] != points[0] {
		t.Fatalf("expected first point to be preserved")
	}
	if got[len(got)-1] != points[len(points)-1] {
		t.Fatalf("expected last point to be preserved")
	}
}

func TestSampleActivityRoutePointsReturnsCopyWhenWithinLimit(t *testing.T) {
	points := []ActivityRoutePoint{{Lat: 10, Lon: 20}, {Lat: 11, Lon: 21}}
	got := sampleActivityRoutePoints(points, 10)
	if len(got) != len(points) {
		t.Fatalf("expected %d points, got %d", len(points), len(got))
	}

	got[0].Lat = 99
	if points[0].Lat == 99 {
		t.Fatalf("expected sampled slice to be a copy")
	}
}
