package web

import (
	"math"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestBuildRidePacingView_UsesElapsedDistanceSplits(t *testing.T) {
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	points := make([]gps.Point, 0, 81)
	longitudeStep := 100 / (111195 * math.Cos(48*math.Pi/180))
	when := start
	for i := 0; i <= 80; i++ {
		quarter := i / 20
		if quarter > 3 {
			quarter = 3
		}
		points = append(points, gps.Point{
			Lat: 48, Lon: 11 + float64(i)*longitudeStep, Time: when,
			Power: float64(200 + quarter*20), HasPower: true,
			HeartRate: float64(140 + quarter*5), HasHeartRate: true,
		})
		if i < 80 {
			switch i / 20 {
			case 0:
				when = when.Add(10 * time.Second)
			case 1:
				when = when.Add(12 * time.Second)
			case 2:
				when = when.Add(11 * time.Second)
			case 3:
				when = when.Add(8 * time.Second)
			}
		}
	}
	view := buildRidePacingView(storage.Activity{Type: "Ride", Distance: 8000}, points)
	if view == nil {
		t.Fatal("expected pacing analysis")
	}
	if len(view.Splits) != 4 {
		t.Fatalf("expected four splits, got %d", len(view.Splits))
	}
	for i, want := range []string{"3:20", "4:00", "3:40", "2:40"} {
		if got := view.Splits[i].Duration; got != want {
			t.Errorf("split %d duration: got %q, want %q", i+1, got, want)
		}
	}
	if view.FastestSplit != "Quarter 4" || view.FinishChange != "+25%" {
		t.Fatalf("unexpected finish comparison: fastest=%q change=%q", view.FastestSplit, view.FinishChange)
	}
	if view.FastestFiveKM != "8:20" {
		t.Fatalf("unexpected quickest 5 km: %q", view.FastestFiveKM)
	}
	if !view.Splits[3].HasPower || view.Splits[3].Power != "260 W" || view.Splits[3].HeartRate != "155 bpm" {
		t.Fatalf("unexpected sensor averages for final split: %+v", view.Splits[3])
	}
}

func TestBuildRidePacingView_RejectsIncompleteTrace(t *testing.T) {
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 48, Lon: 11, Time: start},
		{Lat: 48, Lon: 11.1, Time: start.Add(15 * time.Minute)},
	}
	if view := buildRidePacingView(storage.Activity{Type: "Ride", Distance: 20000}, points); view != nil {
		t.Fatalf("expected no analysis for incomplete GPS trace, got %+v", view)
	}
	if view := buildRidePacingView(storage.Activity{Type: "Run", Distance: 8000}, points); view != nil {
		t.Fatalf("expected ride-only analysis, got %+v", view)
	}
}

func TestBuildRidePacingView_CountsPauseInElapsedSplit(t *testing.T) {
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	longitudeStep := 100 / (111195 * math.Cos(48*math.Pi/180))
	points := make([]gps.Point, 0, 82)
	for i := 0; i <= 80; i++ {
		when := start.Add(time.Duration(i*10) * time.Second)
		if i > 10 {
			when = when.Add(30 * time.Second)
		}
		point := gps.Point{Lat: 48, Lon: 11 + float64(i)*longitudeStep, Time: when}
		points = append(points, point)
		if i == 10 {
			point.Time = point.Time.Add(30 * time.Second)
			points = append(points, point)
		}
	}
	view := buildRidePacingView(storage.Activity{Type: "Ride", Distance: 8000}, points)
	if view == nil {
		t.Fatal("expected pacing analysis")
	}
	if got := view.Splits[0].Duration; got != "3:50" {
		t.Fatalf("expected 30-second pause in opening split, got %q", got)
	}
}
