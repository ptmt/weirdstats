package web

import (
	"fmt"
	"math"
	"time"

	"weirdstats/internal/gps"
)

const (
	heartRateChangeMinDeltaBPM         = 35.0
	heartRateChangeMinRateBPMPerMinute = 45.0
	heartRateChangeMinDuration         = 20 * time.Second
	heartRateChangeMaxDuration         = 90 * time.Second
	heartRateChangeMaxSampleGap        = 5 * time.Second
	heartRateChangeMinRawSamples       = 8
	heartRateChangeInitialIgnore       = 60 * time.Second
	heartRateChangeMinHighBPM          = 160.0
	heartRateMinValidBPM               = 30.0
	heartRateMaxValidBPM               = 240.0
	heartRateEndpointToleranceBPM      = 8.0
)

const (
	heartRateChangeDirectionRise = "rise"
	heartRateChangeDirectionDrop = "drop"
)

type heartRateChangeFact struct {
	Direction        string
	StartBPM         float64
	EndBPM           float64
	DeltaBPM         float64
	RateBPMPerMinute float64
	Duration         time.Duration
	StartIndex       int
	EndIndex         int
	StartLat         float64
	StartLon         float64
	EndLat           float64
	EndLon           float64
	Color            string
}

type heartRateSample struct {
	index     int
	Time      time.Time
	Lat       float64
	Lon       float64
	HeartRate float64
}

func detectHeartRateChangeFact(points []gps.Point) heartRateChangeFact {
	samples := heartRateSamples(points)
	if len(samples) < heartRateChangeMinRawSamples {
		return heartRateChangeFact{}
	}

	best := heartRateChangeFact{}
	for start := 0; start < len(samples); start++ {
		for end := start + heartRateChangeMinRawSamples - 1; end < len(samples); end++ {
			duration := samples[end].Time.Sub(samples[start].Time)
			if duration < heartRateChangeMinDuration {
				continue
			}
			if duration > heartRateChangeMaxDuration {
				break
			}
			if !heartRateWindowHasSufficientQuality(samples, start, end) {
				continue
			}
			if candidate := heartRateChangeCandidate(samples, start, end); candidate.Duration > 0 {
				if heartRateChangeFactBetter(candidate, best) {
					best = candidate
				}
			}
		}
	}
	return best
}

func heartRateSamples(points []gps.Point) []heartRateSample {
	if len(points) == 0 {
		return nil
	}

	ignoreBefore := points[0].Time.Add(heartRateChangeInitialIgnore)
	samples := make([]heartRateSample, 0, len(points))
	for idx, point := range points {
		if !point.HasHeartRate || !validHeartRate(point.HeartRate) {
			continue
		}
		if point.Time.Before(ignoreBefore) {
			continue
		}
		samples = append(samples, heartRateSample{
			index:     idx,
			Time:      point.Time,
			Lat:       point.Lat,
			Lon:       point.Lon,
			HeartRate: point.HeartRate,
		})
	}
	return samples
}

func validHeartRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= heartRateMinValidBPM && value <= heartRateMaxValidBPM
}

func heartRateWindowHasSufficientQuality(samples []heartRateSample, start, end int) bool {
	if start < 0 || end >= len(samples) || start >= end {
		return false
	}
	if end-start+1 < heartRateChangeMinRawSamples {
		return false
	}
	for idx := start + 1; idx <= end; idx++ {
		gap := samples[idx].Time.Sub(samples[idx-1].Time)
		if gap <= 0 || gap > heartRateChangeMaxSampleGap {
			return false
		}
	}
	return true
}

func heartRateChangeCandidate(samples []heartRateSample, start, end int) heartRateChangeFact {
	startBPM := samples[start].HeartRate
	endBPM := samples[end].HeartRate
	delta := endBPM - startBPM
	duration := samples[end].Time.Sub(samples[start].Time)
	if duration <= 0 {
		return heartRateChangeFact{}
	}

	direction := ""
	if delta >= heartRateChangeMinDeltaBPM && endBPM >= heartRateChangeMinHighBPM {
		direction = heartRateChangeDirectionRise
	} else if -delta >= heartRateChangeMinDeltaBPM && startBPM >= heartRateChangeMinHighBPM {
		direction = heartRateChangeDirectionDrop
	} else {
		return heartRateChangeFact{}
	}

	deltaAbs := math.Abs(delta)
	rate := deltaAbs / duration.Minutes()
	if rate < heartRateChangeMinRateBPMPerMinute {
		return heartRateChangeFact{}
	}
	if !heartRateWindowTrendOK(samples, start, end, direction, deltaAbs) {
		return heartRateChangeFact{}
	}
	if !heartRateWindowEndpointsStable(samples, start, end, direction) {
		return heartRateChangeFact{}
	}

	return newHeartRateChangeFact(direction, samples[start], samples[end], deltaAbs, rate, duration)
}

func heartRateWindowTrendOK(samples []heartRateSample, start, end int, direction string, deltaAbs float64) bool {
	opposite := 0.0
	aligned := 0.0
	for idx := start + 1; idx <= end; idx++ {
		delta := samples[idx].HeartRate - samples[idx-1].HeartRate
		switch direction {
		case heartRateChangeDirectionRise:
			if delta > 0 {
				aligned += delta
			} else {
				opposite += -delta
			}
		case heartRateChangeDirectionDrop:
			if delta < 0 {
				aligned += -delta
			} else {
				opposite += delta
			}
		}
	}

	maxOpposite := math.Max(12.0, deltaAbs*0.30)
	if opposite > maxOpposite {
		return false
	}
	return aligned >= deltaAbs*0.75
}

func heartRateWindowEndpointsStable(samples []heartRateSample, start, end int, direction string) bool {
	startAvg, ok := heartRateEdgeAverage(samples, start, start+2)
	if !ok {
		return false
	}
	endAvg, ok := heartRateEdgeAverage(samples, end-2, end)
	if !ok {
		return false
	}

	startBPM := samples[start].HeartRate
	endBPM := samples[end].HeartRate
	switch direction {
	case heartRateChangeDirectionRise:
		return startAvg <= startBPM+heartRateEndpointToleranceBPM &&
			endAvg >= endBPM-heartRateEndpointToleranceBPM
	case heartRateChangeDirectionDrop:
		return startAvg >= startBPM-heartRateEndpointToleranceBPM &&
			endAvg <= endBPM+heartRateEndpointToleranceBPM
	default:
		return false
	}
}

func heartRateEdgeAverage(samples []heartRateSample, start, end int) (float64, bool) {
	if start < 0 || end >= len(samples) || start > end {
		return 0, false
	}
	total := 0.0
	for idx := start; idx <= end; idx++ {
		total += samples[idx].HeartRate
	}
	return total / float64(end-start+1), true
}

func newHeartRateChangeFact(direction string, start, end heartRateSample, delta, rate float64, duration time.Duration) heartRateChangeFact {
	color := "#ef4444"
	if direction == heartRateChangeDirectionDrop {
		color = "#6366f1"
	}
	return heartRateChangeFact{
		Direction:        direction,
		StartBPM:         start.HeartRate,
		EndBPM:           end.HeartRate,
		DeltaBPM:         delta,
		RateBPMPerMinute: rate,
		Duration:         duration,
		StartIndex:       start.index,
		EndIndex:         end.index,
		StartLat:         start.Lat,
		StartLon:         start.Lon,
		EndLat:           end.Lat,
		EndLon:           end.Lon,
		Color:            color,
	}
}

func heartRateChangeFactBetter(candidate, best heartRateChangeFact) bool {
	if best.Duration <= 0 {
		return true
	}
	if math.Abs(candidate.RateBPMPerMinute-best.RateBPMPerMinute) > 0.01 {
		return candidate.RateBPMPerMinute > best.RateBPMPerMinute
	}
	if math.Abs(candidate.DeltaBPM-best.DeltaBPM) > 0.01 {
		return candidate.DeltaBPM > best.DeltaBPM
	}
	return candidate.Duration < best.Duration
}

func buildHeartRateChangePart(fact heartRateChangeFact) string {
	if fact.Duration <= 0 {
		return ""
	}
	return fmt.Sprintf("HR %s: %s", fact.Direction, heartRateChangeSummary(fact))
}

func heartRateChangeTitle(fact heartRateChangeFact) string {
	if fact.Direction == heartRateChangeDirectionDrop {
		return "HR drop"
	}
	return "HR rise"
}

func heartRateChangeSummary(fact heartRateChangeFact) string {
	if fact.Duration <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"%.0f-%.0fbpm in %ss (%sbpm/min)",
		fact.StartBPM,
		fact.EndBPM,
		formatCompactNumber(fact.Duration.Seconds(), 1),
		formatCompactNumber(fact.RateBPMPerMinute, 0),
	)
}

func heartRateChangePathPoints(points []gps.Point, fact heartRateChangeFact) []routePreviewPoint {
	return pathPointsBetweenIndices(points, fact.StartIndex, fact.EndIndex)
}
