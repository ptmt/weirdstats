package processor

import (
	"context"
	"math"
	"sort"
	"strings"

	"weirdstats/internal/storage"
)

const (
	effortVersion       = 1
	effortHRRefFallback = 120.0
	effortHRWindow      = 50
	effortHRFactorMin   = 0.6
	effortHRFactorMax   = 2.5
)

var effortSportFactors = map[string]float64{
	"swim":             2.2,
	"openwaterswim":    2.2,
	"poolswim":         2.2,
	"run":              2.0,
	"trailrun":         2.0,
	"virtualrun":       2.0,
	"treadmill":        2.0,
	"ride":             1.6,
	"virtualride":      1.6,
	"mountainbikeride": 1.6,
	"gravelride":       1.6,
	"ebikeride":        1.5,
	"walk":             1.0,
	"hike":             1.8,
	"workout":          1.7,
	"weighttraining":   1.6,
	"strengthtraining": 1.6,
	"crossfit":         1.7,
	"hiit":             1.8,
	"rowing":           1.7,
	"rowergometer":     1.7,
	"kayaking":         1.5,
	"canoeing":         1.5,
	"alpineski":        1.6,
	"nordicski":        1.6,
	"backcountryski":   1.7,
	"snowboard":        1.6,
	"snowshoe":         1.6,
	"yoga":             0.7,
	"pilates":          0.7,
	"elliptical":       1.5,
	"stairstepper":     1.7,
	"stairclimber":     1.7,
}

func computeEffort(ctx context.Context, store *storage.Store, activity storage.Activity) (float64, int, error) {
	durationMinutes := float64(activity.MovingTime) / 60.0
	if durationMinutes <= 0 {
		return 0, effortVersion, nil
	}

	sportFactor := effortSportFactor(activity.Type)
	hrFactor := 1.0
	if activity.AverageHeartRate > 0 {
		hrRef, err := effortHRRef(ctx, store, activity)
		if err != nil {
			return 0, effortVersion, err
		}
		if hrRef <= 0 {
			hrRef = effortHRRefFallback
		}
		ratio := activity.AverageHeartRate / hrRef
		hrFactor = clampFloat(math.Pow(ratio, 2), effortHRFactorMin, effortHRFactorMax)
	}

	return durationMinutes * sportFactor * hrFactor, effortVersion, nil
}

func effortHRRef(ctx context.Context, store *storage.Store, activity storage.Activity) (float64, error) {
	values, err := store.ListRecentAverageHeartrates(ctx, activity.UserID, activity.StartTime, effortHRWindow)
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return effortHRRefFallback, nil
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid], nil
	}
	return (values[mid-1] + values[mid]) / 2, nil
}

func effortSportFactor(activityType string) float64 {
	key := normalizeActivityType(activityType)
	if factor, ok := effortSportFactors[key]; ok {
		return factor
	}
	return 1.0
}

func normalizeActivityType(value string) string {
	if value == "" {
		return ""
	}
	normalized := strings.ToLower(value)
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
