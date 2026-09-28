package web

import (
	"math"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestBuildDraftingView_RepeatedPowerDropsAtSteadySpeed(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) {
		if second/120%2 == 1 {
			return 200, 10, 0
		}
		return 300, 10, 0
	})
	meter := true
	view := buildDraftingView(storage.Activity{Type: "Ride", DeviceWatts: &meter, AthleteCount: 4}, points)
	if view == nil || view.Count != 2 {
		t.Fatalf("expected two power drops, got %+v", view)
	}
	if view.Confidence < 75 || view.Confidence > 90 {
		t.Fatalf("unexpected confidence %d", view.Confidence)
	}
	if view.CandidateTime == "" {
		t.Fatal("missing matched window duration")
	}
	if len(view.Candidates) != 2 || view.Candidates[0].PowerDrop != "300 → 200 W (−33%)" {
		t.Fatalf("unexpected candidates %+v", view.Candidates)
	}
}

func TestBuildDraftingView_ShortShiftNeedsShortWindow(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) {
		if second >= 67 && second < 81 {
			return 195, 10, 0
		}
		return 260, 10, 0
	})
	meter := true
	activity := storage.Activity{Type: "Ride", DeviceWatts: &meter}
	short := buildDraftingViewWithOptions(activity, points, draftingOptions{windowSeconds: 10, minDropPercent: 12})
	long := buildDraftingViewWithOptions(activity, points, draftingOptions{windowSeconds: 30, minDropPercent: 12})
	if short == nil || short.Count == 0 || long == nil || long.Count != 0 {
		t.Fatalf("short shift not isolated by window: short=%+v long=%+v", short, long)
	}
	if short.Confidence > 55 {
		t.Fatalf("brief isolated clue scored too highly: %+v", short)
	}
}

func TestBuildDraftingView_PowerDropSensitivity(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) {
		if second >= 70 && second < 110 {
			return 220, 10, 0
		}
		return 250, 10, 0
	})
	meter := true
	activity := storage.Activity{Type: "Ride", DeviceWatts: &meter}
	sensitive := buildDraftingViewWithOptions(activity, points, draftingOptions{windowSeconds: 15, minDropPercent: 8})
	strict := buildDraftingViewWithOptions(activity, points, draftingOptions{windowSeconds: 15, minDropPercent: 20})
	if sensitive == nil || sensitive.Count == 0 || strict == nil || strict.Count != 0 {
		t.Fatalf("minimum drop did not change detection: sensitive=%+v strict=%+v", sensitive, strict)
	}
}

func TestBuildDraftingView_RejectsSpeedAndGradeChanges(t *testing.T) {
	meter := true
	for _, test := range []struct {
		name  string
		point func(int) (float64, float64, float64)
	}{
		{"slowing down", func(second int) (float64, float64, float64) {
			if second >= 120 {
				return 200, 8, 0
			}
			return 300, 10, 0
		}},
		{"descending", func(second int) (float64, float64, float64) {
			if second >= 120 {
				return 200, 10, -4
			}
			return 300, 10, 0
		}},
		{"speed surges with the same average", func(second int) (float64, float64, float64) {
			if second >= 120 {
				if second/10%2 == 0 {
					return 200, 9.2, 0
				}
				return 200, 10.8, 0
			}
			return 300, 10, 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := buildDraftingView(storage.Activity{Type: "Ride", DeviceWatts: &meter}, draftingTestPoints(test.point))
			if view == nil || view.Count != 0 {
				t.Fatalf("expected no draft candidate, got %+v", view)
			}
		})
	}
}

func TestBuildDraftingView_SmallerRepeatableDropGetsModerateScore(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) {
		if second/120%2 == 1 {
			return 210, 10, 0
		}
		return 250, 10, 0
	})
	meter := true
	view := buildDraftingView(storage.Activity{Type: "Ride", DeviceWatts: &meter, AthleteCount: 3}, points)
	if view == nil || view.Count != 2 || view.Confidence < 50 || view.Confidence >= 75 {
		t.Fatalf("expected two moderate clues, got %+v", view)
	}
}

func TestBuildDraftingView_RequiresMeasuredPower(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) { return 220, 10, 0 })
	falseValue := false
	for _, source := range []*bool{nil, &falseValue} {
		view := buildDraftingView(storage.Activity{Type: "Ride", DeviceWatts: source}, points)
		if view == nil || view.Count != 0 || view.Status == "Possible draft-like shifts" {
			t.Fatalf("expected power source gate, got %+v", view)
		}
	}
}

func TestBuildDraftingView_StopsPromptingAfterSourceCheck(t *testing.T) {
	points := draftingTestPoints(func(second int) (float64, float64, float64) { return 220, 10, 0 })
	view := buildDraftingView(storage.Activity{Type: "Ride", PowerSourceChecked: true}, points)
	if view == nil || view.NeedsRefresh || view.Status != "Power source unavailable from Strava" {
		t.Fatalf("expected checked-but-unavailable state, got %+v", view)
	}
}

func draftingTestPoints(values func(int) (float64, float64, float64)) []gps.Point {
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	longitudeStep := 10 / (111195 * math.Cos(48*math.Pi/180))
	points := make([]gps.Point, 481)
	for second := range points {
		power, speed, grade := values(second)
		points[second] = gps.Point{
			Lat: 48, Lon: 11 + float64(second)*longitudeStep,
			Time:  start.Add(time.Duration(second) * time.Second),
			Speed: speed, Power: power, HasPower: true,
			Grade: grade, HasGrade: true,
		}
	}
	return points
}
