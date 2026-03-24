package web

import (
	"sort"
	"strings"

	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

const stravaPostedFactLimit = 4

type weirdStatsFactCandidate struct {
	ID           string
	Part         string
	BasePriority int
	DefaultOrder int
	Metrics      []storage.ActivityFactMetric
}

type scoredWeirdStatsFactCandidate struct {
	candidate weirdStatsFactCandidate
	score     int
}

func buildPrioritizedWeirdStatsLine(
	statsSnapshot stats.StopStats,
	rideFact rideSegmentFact,
	speedFacts []speedMilestoneFact,
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
	histories map[string]storage.UserFactMetricHistory,
) string {
	candidates := buildWeirdStatsFactCandidates(statsSnapshot, rideFact, speedFacts, coffeeFact, routeFact, roadFact)
	candidates = prioritizeStravaFactCandidates(candidates, histories, stravaPostedFactLimit)
	if len(candidates) == 0 {
		return ""
	}

	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, candidate.Part)
	}
	return strings.Join(parts, " · ")
}

func buildWeirdStatsFactCandidates(
	statsSnapshot stats.StopStats,
	rideFact rideSegmentFact,
	speedFacts []speedMilestoneFact,
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
) []weirdStatsFactCandidate {
	candidates := make([]weirdStatsFactCandidate, 0, 10)

	if part := buildRideSegmentPart(rideFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactLongestSegment,
			Part:         part,
			BasePriority: 600,
			DefaultOrder: 0,
			Metrics:      rideSegmentFactMetrics(rideFact),
		})
	}

	for _, speedFact := range speedFacts {
		if part := buildSpeedMilestonePart(speedFact); part != "" {
			candidates = append(candidates, weirdStatsFactCandidate{
				ID:           speedFact.FactID,
				Part:         part,
				BasePriority: speedMilestoneBasePriority(speedFact.FactID),
				DefaultOrder: 1 + speedFact.DefaultOrder,
				Metrics:      speedMilestoneFactMetrics([]speedMilestoneFact{speedFact}),
			})
		}
	}

	if part := buildCoffeeStopPart(coffeeFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactCoffeeStop,
			Part:         part,
			BasePriority: 540,
			DefaultOrder: 5,
			Metrics:      coffeeStopFactMetrics(coffeeFact),
		})
	}

	if part := buildRouteHighlightPart(routeFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactRouteHighlights,
			Part:         part,
			BasePriority: 520,
			DefaultOrder: 6,
			Metrics:      routeHighlightFactMetrics(routeFact),
		})
	}

	roadCount := roadFact.Count
	if roadCount <= 0 {
		roadCount = statsSnapshot.RoadCrossingCount
	}
	if part := buildRoadCrossingPartWithCount(roadCount, roadFact.Roads); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactRoadCrossings,
			Part:         part,
			BasePriority: 400,
			DefaultOrder: 7,
			Metrics:      roadCrossingFactMetrics(statsSnapshot, roadFact),
		})
	}

	if part := buildStopSummaryPart(statsSnapshot); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactStopSummary,
			Part:         part,
			BasePriority: 350,
			DefaultOrder: 8,
			Metrics:      stopSummaryFactMetrics(statsSnapshot),
		})
	}

	if part := buildTrafficLightStopsPart(statsSnapshot.TrafficLightStopCount); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactTrafficLightStops,
			Part:         part,
			BasePriority: 330,
			DefaultOrder: 9,
			Metrics:      trafficLightStopFactMetrics(statsSnapshot),
		})
	}

	return candidates
}

func prioritizeStravaFactCandidates(
	candidates []weirdStatsFactCandidate,
	histories map[string]storage.UserFactMetricHistory,
	limit int,
) []weirdStatsFactCandidate {
	if len(candidates) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}

	scored := make([]scoredWeirdStatsFactCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		scored = append(scored, scoredWeirdStatsFactCandidate{
			candidate: candidate,
			score:     scoreWeirdStatsFactCandidate(candidate, histories),
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].candidate.BasePriority != scored[j].candidate.BasePriority {
			return scored[i].candidate.BasePriority > scored[j].candidate.BasePriority
		}
		return scored[i].candidate.DefaultOrder < scored[j].candidate.DefaultOrder
	})

	selected := make([]weirdStatsFactCandidate, 0, limit)
	for _, item := range scored[:limit] {
		selected = append(selected, item.candidate)
	}
	return selected
}

func collectWeirdStatsCandidateMetrics(candidates []weirdStatsFactCandidate) []storage.ActivityFactMetric {
	metrics := make([]storage.ActivityFactMetric, 0, len(candidates)*2)
	for _, candidate := range candidates {
		metrics = append(metrics, candidate.Metrics...)
	}
	return metrics
}

func scoreWeirdStatsFactCandidate(candidate weirdStatsFactCandidate, histories map[string]storage.UserFactMetricHistory) int {
	score := candidate.BasePriority
	if histories == nil || len(candidate.Metrics) == 0 {
		return score
	}

	switch candidate.ID {
	case weirdStatsFactCoffeeStop, weirdStatsFactRouteHighlights:
		return score + scorePOINovelty(candidate.Metrics, histories)
	default:
		return score + scoreNumericNovelty(candidate.Metrics, histories)
	}
}

func scorePOINovelty(metrics []storage.ActivityFactMetric, histories map[string]storage.UserFactMetricHistory) int {
	score := 0
	for _, metric := range metrics {
		if history, ok := histories[metric.FactID+":"+metric.MetricID]; ok && history.AllTimeSeenCount > 0 {
			score -= 70
			continue
		}
		score += 110
	}
	return score
}

func scoreNumericNovelty(metrics []storage.ActivityFactMetric, histories map[string]storage.UserFactMetricHistory) int {
	best := 0
	haveBest := false
	for _, metric := range metrics {
		value := scoreNumericMetric(metric, histories)
		if !haveBest || value > best {
			best = value
			haveBest = true
		}
	}
	if !haveBest {
		return 0
	}
	return best
}

func scoreNumericMetric(metric storage.ActivityFactMetric, histories map[string]storage.UserFactMetricHistory) int {
	history, ok := histories[metric.FactID+":"+metric.MetricID]
	if !ok || history.AllTimeSeenCount == 0 {
		return 220
	}
	if history.AllTimeBestValue <= 0 {
		return 0
	}
	if metric.MetricValue > history.AllTimeBestValue {
		improvementRatio := improvementRatio(metric.MetricValue, history.AllTimeBestValue)
		score := 320
		switch {
		case improvementRatio >= 0.25:
			score += 60
		case improvementRatio >= 0.10:
			score += 35
		case improvementRatio > 0:
			score += 15
		}
		return score
	}
	if history.YearSeenCount > 0 && metric.MetricValue > history.YearBestValue && history.YearBestValue > 0 {
		improvementRatio := improvementRatio(metric.MetricValue, history.YearBestValue)
		score := 210
		switch {
		case improvementRatio >= 0.25:
			score += 40
		case improvementRatio >= 0.10:
			score += 20
		case improvementRatio > 0:
			score += 10
		}
		return score
	}

	ratio := metric.MetricValue / history.AllTimeBestValue
	switch {
	case ratio >= 0.98:
		return 90
	case ratio >= 0.90:
		return 45
	case ratio >= 0.75:
		return 10
	case ratio >= 0.50:
		return -55
	default:
		return -140
	}
}

func improvementRatio(value, baseline float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return (value - baseline) / baseline
}

func speedMilestoneBasePriority(factID string) int {
	switch factID {
	case weirdStatsFactAcceleration040:
		return 490
	case weirdStatsFactAcceleration030:
		return 480
	case weirdStatsFactDeceleration400:
		return 470
	case weirdStatsFactDeceleration300:
		return 460
	default:
		return 450
	}
}

func buildStopSummaryPart(statsSnapshot stats.StopStats) string {
	if statsSnapshot.StopCount <= 0 {
		return ""
	}
	part := formatCountLabel(statsSnapshot.StopCount, "stop", "stops")
	if statsSnapshot.StopTotalSeconds > 0 {
		part += " (" + formatDuration(statsSnapshot.StopTotalSeconds) + " total)"
	}
	return part
}

func buildTrafficLightStopsPart(count int) string {
	if count <= 0 {
		return ""
	}
	return formatCountLabel(count, "at lights", "at lights")
}
