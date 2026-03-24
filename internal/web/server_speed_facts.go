package web

import (
	"fmt"
	"math"
	"time"

	"weirdstats/internal/gps"
)

const (
	speedMilestoneStopThresholdMPS    = 0.5
	speedMilestoneMaxAccelerationMPS2 = 3.5
	speedMilestoneMaxDecelerationMPS2 = 5.0
	speedMilestoneMaxDownhillGradePct = 3.0
)

type speedMilestoneDefinition struct {
	FactID       string
	Label        string
	StartKPH     float64
	EndKPH       float64
	DefaultOrder int
	Color        string
}

type speedMilestoneFact struct {
	FactID       string
	Label        string
	StartKPH     float64
	EndKPH       float64
	Duration     time.Duration
	StartIndex   int
	EndIndex     int
	StartLat     float64
	StartLon     float64
	EndLat       float64
	EndLon       float64
	DefaultOrder int
	Color        string
}

var speedMilestoneDefinitions = []speedMilestoneDefinition{
	{
		FactID:       weirdStatsFactAcceleration030,
		Label:        "0 to 30 km/h",
		StartKPH:     0,
		EndKPH:       30,
		DefaultOrder: 0,
		Color:        "#10b981",
	},
	{
		FactID:       weirdStatsFactAcceleration040,
		Label:        "0 to 40 km/h",
		StartKPH:     0,
		EndKPH:       40,
		DefaultOrder: 1,
		Color:        "#14b8a6",
	},
	{
		FactID:       weirdStatsFactDeceleration400,
		Label:        "40 to 0 km/h",
		StartKPH:     40,
		EndKPH:       0,
		DefaultOrder: 2,
		Color:        "#f97316",
	},
	{
		FactID:       weirdStatsFactDeceleration300,
		Label:        "30 to 0 km/h",
		StartKPH:     30,
		EndKPH:       0,
		DefaultOrder: 3,
		Color:        "#fb7185",
	},
}

var speedMilestoneDefinitionsByFactID = func() map[string]speedMilestoneDefinition {
	items := make(map[string]speedMilestoneDefinition, len(speedMilestoneDefinitions))
	for _, item := range speedMilestoneDefinitions {
		items[item.FactID] = item
	}
	return items
}()

func detectSpeedMilestoneFacts(activityType string, points []gps.Point) []speedMilestoneFact {
	if !isRideType(activityType) || len(points) < 2 {
		return nil
	}

	facts := make([]speedMilestoneFact, 0, len(speedMilestoneDefinitions))
	for _, definition := range speedMilestoneDefinitions {
		fact := detectSpeedMilestoneFact(points, definition)
		if fact.Duration <= 0 {
			continue
		}
		facts = append(facts, fact)
	}
	return facts
}

func filterSpeedMilestoneFactsBySettings(facts []speedMilestoneFact, settings map[string]bool) []speedMilestoneFact {
	if len(facts) == 0 {
		return nil
	}

	filtered := make([]speedMilestoneFact, 0, len(facts))
	for _, fact := range facts {
		if weirdStatsFactEnabled(settings, fact.FactID) {
			filtered = append(filtered, fact)
		}
	}
	return filtered
}

func detectSpeedMilestoneFact(points []gps.Point, definition speedMilestoneDefinition) speedMilestoneFact {
	startThresholdMPS := speedMilestoneThresholdMPS(definition.StartKPH)
	endThresholdMPS := speedMilestoneThresholdMPS(definition.EndKPH)
	if definition.StartKPH < definition.EndKPH {
		return detectAccelerationMilestoneFact(points, definition, startThresholdMPS, endThresholdMPS)
	}
	return detectDecelerationMilestoneFact(points, definition, startThresholdMPS, endThresholdMPS)
}

func detectAccelerationMilestoneFact(points []gps.Point, definition speedMilestoneDefinition, startThresholdMPS, endThresholdMPS float64) speedMilestoneFact {
	best := speedMilestoneFact{}
	var startCandidate thresholdCrossing
	active := false
	minDuration := speedMilestoneMinDuration(startThresholdMPS, endThresholdMPS, speedMilestoneMaxAccelerationMPS2)

	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]
		if !curr.Time.After(prev.Time) {
			continue
		}

		if crossing, ok := upwardThresholdCrossing(prev, curr, startThresholdMPS); ok {
			startCandidate = crossing
			startCandidate.index = i - 1
			active = true
		}
		if !active {
			continue
		}

		if crossing, ok := upwardThresholdCrossing(prev, curr, endThresholdMPS); ok {
			crossing.index = i
			duration := crossing.Time.Sub(startCandidate.Time)
			if duration <= 0 || duration < minDuration {
				active = false
				continue
			}
			if !accelerationMilestoneTrusted(points, startCandidate, crossing) {
				active = false
				continue
			}
			candidate := newSpeedMilestoneFact(definition, duration, startCandidate, crossing)
			if best.Duration <= 0 || candidate.Duration < best.Duration {
				best = candidate
			}
			active = false
			continue
		}

		if _, ok := downwardThresholdCrossing(prev, curr, startThresholdMPS); ok {
			active = false
		}
	}

	return best
}

func detectDecelerationMilestoneFact(points []gps.Point, definition speedMilestoneDefinition, startThresholdMPS, endThresholdMPS float64) speedMilestoneFact {
	best := speedMilestoneFact{}
	var startCandidate thresholdCrossing
	active := false
	minDuration := speedMilestoneMinDuration(startThresholdMPS, endThresholdMPS, speedMilestoneMaxDecelerationMPS2)

	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]
		if !curr.Time.After(prev.Time) {
			continue
		}

		if crossing, ok := downwardThresholdCrossing(prev, curr, startThresholdMPS); ok {
			startCandidate = crossing
			startCandidate.index = i - 1
			active = true
		}
		if !active {
			continue
		}

		if crossing, ok := downwardThresholdCrossing(prev, curr, endThresholdMPS); ok {
			crossing.index = i
			duration := crossing.Time.Sub(startCandidate.Time)
			if duration <= 0 || duration < minDuration {
				active = false
				continue
			}
			candidate := newSpeedMilestoneFact(definition, duration, startCandidate, crossing)
			if best.Duration <= 0 || candidate.Duration < best.Duration {
				best = candidate
			}
			active = false
			continue
		}

		if _, ok := upwardThresholdCrossing(prev, curr, startThresholdMPS); ok {
			active = false
		}
	}

	return best
}

type thresholdCrossing struct {
	Time     time.Time
	Lat      float64
	Lon      float64
	Grade    float64
	HasGrade bool
	index    int
}

func upwardThresholdCrossing(prev, curr gps.Point, thresholdMPS float64) (thresholdCrossing, bool) {
	if curr.Speed <= prev.Speed {
		return thresholdCrossing{}, false
	}
	if prev.Speed > thresholdMPS || curr.Speed < thresholdMPS {
		return thresholdCrossing{}, false
	}
	return interpolateThresholdCrossing(prev, curr, thresholdMPS), true
}

func downwardThresholdCrossing(prev, curr gps.Point, thresholdMPS float64) (thresholdCrossing, bool) {
	if curr.Speed >= prev.Speed {
		return thresholdCrossing{}, false
	}
	if prev.Speed < thresholdMPS || curr.Speed > thresholdMPS {
		return thresholdCrossing{}, false
	}
	return interpolateThresholdCrossing(prev, curr, thresholdMPS), true
}

func interpolateThresholdCrossing(prev, curr gps.Point, thresholdMPS float64) thresholdCrossing {
	if !curr.Time.After(prev.Time) || curr.Speed == prev.Speed {
		return thresholdCrossing{
			Time:     curr.Time,
			Lat:      curr.Lat,
			Lon:      curr.Lon,
			Grade:    curr.Grade,
			HasGrade: curr.HasGrade,
		}
	}
	fraction := (thresholdMPS - prev.Speed) / (curr.Speed - prev.Speed)
	if fraction < 0 {
		fraction = 0
	} else if fraction > 1 {
		fraction = 1
	}
	delta := curr.Time.Sub(prev.Time)
	return thresholdCrossing{
		Time: prev.Time.Add(time.Duration(float64(delta) * fraction)),
		Lat:  prev.Lat + (curr.Lat-prev.Lat)*fraction,
		Lon:  prev.Lon + (curr.Lon-prev.Lon)*fraction,
		Grade: func() float64 {
			if !prev.HasGrade || !curr.HasGrade {
				return 0
			}
			return prev.Grade + (curr.Grade-prev.Grade)*fraction
		}(),
		HasGrade: prev.HasGrade && curr.HasGrade,
	}
}

func newSpeedMilestoneFact(definition speedMilestoneDefinition, duration time.Duration, start, end thresholdCrossing) speedMilestoneFact {
	return speedMilestoneFact{
		FactID:       definition.FactID,
		Label:        definition.Label,
		StartKPH:     definition.StartKPH,
		EndKPH:       definition.EndKPH,
		Duration:     duration,
		StartIndex:   start.index,
		EndIndex:     end.index,
		StartLat:     start.Lat,
		StartLon:     start.Lon,
		EndLat:       end.Lat,
		EndLon:       end.Lon,
		DefaultOrder: definition.DefaultOrder,
		Color:        definition.Color,
	}
}

func speedMilestoneThresholdMPS(kph float64) float64 {
	if kph <= 0 {
		return speedMilestoneStopThresholdMPS
	}
	return kph / 3.6
}

func speedMilestoneMinDuration(startThresholdMPS, endThresholdMPS, maxDeltaMPS2 float64) time.Duration {
	if maxDeltaMPS2 <= 0 {
		return 0
	}
	speedDelta := math.Abs(endThresholdMPS - startThresholdMPS)
	if speedDelta <= 0 {
		return 0
	}
	seconds := speedDelta / maxDeltaMPS2
	return time.Duration(seconds * float64(time.Second))
}

func accelerationMilestoneTrusted(points []gps.Point, start, end thresholdCrossing) bool {
	avgGrade, ok := averageGradeBetween(points, start, end)
	if !ok {
		return false
	}
	return avgGrade >= -speedMilestoneMaxDownhillGradePct
}

func averageGradeBetween(points []gps.Point, start, end thresholdCrossing) (float64, bool) {
	if start.index < 0 || end.index < 0 || start.index >= len(points) || end.index >= len(points) || start.index > end.index {
		return 0, false
	}

	total := 0.0
	count := 0

	if start.HasGrade {
		total += start.Grade
		count++
	}

	for idx := start.index; idx <= end.index; idx++ {
		if !points[idx].HasGrade {
			continue
		}
		total += points[idx].Grade
		count++
	}

	if end.HasGrade && (end.index != start.index || !start.HasGrade || end.Grade != start.Grade) {
		total += end.Grade
		count++
	}

	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

func buildSpeedMilestonePart(fact speedMilestoneFact) string {
	if fact.Duration <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f-%.0fkmh in %ss", fact.StartKPH, fact.EndKPH, formatCompactNumber(fact.Duration.Seconds(), 1))
}

func speedMilestoneSummary(fact speedMilestoneFact) string {
	return buildSpeedMilestonePart(fact)
}

func speedMilestonePathPoints(points []gps.Point, fact speedMilestoneFact) []routePreviewPoint {
	return pathPointsBetweenIndices(points, fact.StartIndex, fact.EndIndex)
}
