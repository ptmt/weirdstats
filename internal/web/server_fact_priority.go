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
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
	histories map[string]storage.UserFactMetricHistory,
) string {
	candidates := buildWeirdStatsFactCandidates(statsSnapshot, rideFact, coffeeFact, routeFact, roadFact)
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
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
) []weirdStatsFactCandidate {
	candidates := make([]weirdStatsFactCandidate, 0, 6)

	if part := buildRideSegmentPart(rideFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactLongestSegment,
			Part:         part,
			BasePriority: 600,
			DefaultOrder: 0,
			Metrics:      rideSegmentFactMetrics(rideFact),
		})
	}

	if part := buildCoffeeStopPart(coffeeFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactCoffeeStop,
			Part:         part,
			BasePriority: 540,
			DefaultOrder: 1,
			Metrics:      coffeeStopFactMetrics(coffeeFact),
		})
	}

	if part := buildRouteHighlightPart(routeFact); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactRouteHighlights,
			Part:         part,
			BasePriority: 520,
			DefaultOrder: 2,
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
			DefaultOrder: 3,
			Metrics:      roadCrossingFactMetrics(statsSnapshot, roadFact),
		})
	}

	if part := buildStopSummaryPart(statsSnapshot); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactStopSummary,
			Part:         part,
			BasePriority: 350,
			DefaultOrder: 4,
			Metrics:      stopSummaryFactMetrics(statsSnapshot),
		})
	}

	if part := buildTrafficLightStopsPart(statsSnapshot.TrafficLightStopCount); part != "" {
		candidates = append(candidates, weirdStatsFactCandidate{
			ID:           weirdStatsFactTrafficLightStops,
			Part:         part,
			BasePriority: 330,
			DefaultOrder: 5,
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
		if history, ok := histories[metric.FactID+":"+metric.MetricID]; ok && history.SeenCount > 0 {
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
	if !ok || history.SeenCount == 0 {
		return 180
	}
	if history.BestValue <= 0 {
		return 0
	}
	if metric.MetricValue > history.BestValue {
		improvementRatio := (metric.MetricValue - history.BestValue) / history.BestValue
		score := 240
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

	ratio := metric.MetricValue / history.BestValue
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
