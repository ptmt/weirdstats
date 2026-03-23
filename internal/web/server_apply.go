package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/jobs"
	"weirdstats/internal/rules"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

func (s *Server) ApplyActivityRules(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	idStr = strings.TrimSuffix(idStr, "/apply")
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	if _, err := s.store.GetActivityForUser(r.Context(), userID, activityID); err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err := jobs.EnqueueApplyActivityRules(r.Context(), s.store, activityID, userID); err != nil {
		http.Error(w, "failed to enqueue activity apply", http.StatusInternalServerError)
		return
	}
	s.redirectBack(w, r, activityID, "sync+queued")
}

func (s *Server) Apply(ctx context.Context, activityID int64) error {
	return s.applyActivityRules(ctx, activityID)
}

func (s *Server) applyActivityRules(ctx context.Context, activityID int64) error {
	activity, err := s.store.GetActivity(ctx, activityID)
	if err != nil {
		return err
	}

	hide, statsSnapshot, err := s.evaluateHideRules(ctx, activity)
	if err != nil {
		return err
	}
	if err := s.store.UpdateActivityHiddenByRule(ctx, activityID, hide); err != nil {
		return err
	}

	factSettings, err := s.loadWeirdStatsFactSettings(ctx, activity.UserID)
	if err != nil {
		return err
	}
	rideFactEnabled := weirdStatsFactEnabled(factSettings, weirdStatsFactLongestSegment)
	coffeeFactEnabled := weirdStatsFactEnabled(factSettings, weirdStatsFactCoffeeStop)
	routeFactEnabled := weirdStatsFactEnabled(factSettings, weirdStatsFactRouteHighlights)
	roadCrossingFactEnabled := weirdStatsFactEnabled(factSettings, weirdStatsFactRoadCrossings)
	needsRideFacts := rideFactEnabled || coffeeFactEnabled || routeFactEnabled

	baseDescription := activity.Description
	baseHideFromHome := activity.HideFromHome
	rideFact := rideSegmentFact{}
	coffeeFact := coffeeStopFact{}
	routeFact := routeHighlightFact{}
	roadFact := roadCrossingFact{}
	if roadCrossingFactEnabled {
		stops, err := s.store.LoadActivityStops(ctx, activityID)
		if err != nil {
			log.Printf("activity stops load failed (skipping road crossing fact): %v", err)
		} else {
			roadFact = buildRoadCrossingFact(stops)
		}
	}
	if needsRideFacts && isRideType(activity.Type) {
		points, err := s.store.LoadActivityPoints(ctx, activityID)
		if err != nil {
			log.Printf("local activity points load failed (skipping ride fact): %v", err)
		} else {
			if routeFactEnabled {
				routeFact, err = detectRouteHighlightFact(ctx, points, s.overpass)
				if err != nil {
					log.Printf("local route highlight detection failed (skipping route highlights): %v", err)
					routeFact = routeHighlightFact{}
				}
			}
			if rideFactEnabled {
				rideFact = longestRideSegmentFact(activity.Type, points, s.stopOpts)
			}
			if coffeeFactEnabled {
				coffeeFact, err = detectCoffeeStopFact(ctx, activity.Type, points, s.overpass)
				if err != nil {
					log.Printf("local coffee stop detection failed (skipping coffee fact): %v", err)
					coffeeFact = coffeeStopFact{}
				}
			}
		}
	}

	client, clientErr := s.stravaClientForUser(ctx, activity.UserID)
	if clientErr == nil {
		latest, err := client.GetActivity(ctx, activityID)
		if err != nil {
			log.Printf("strava activity fetch failed (using cached description): %v", err)
		} else {
			baseDescription = latest.Description
			baseHideFromHome = latest.HideFromHome
			if needsRideFacts && isRideType(latest.Type) {
				streams, err := client.GetStreams(ctx, activityID)
				if err != nil {
					log.Printf("strava streams fetch failed (using cached ride fact): %v", err)
				} else {
					points := buildPointsFromStreams(latest.StartDate, streams)
					if routeFactEnabled {
						routeFact, err = detectRouteHighlightFact(ctx, points, s.overpass)
						if err != nil {
							log.Printf("strava route highlight detection failed (using cached route highlights): %v", err)
						}
					}
					if rideFactEnabled {
						rideFact = longestRideSegmentFact(latest.Type, points, s.stopOpts)
					}
					if coffeeFactEnabled {
						coffeeFact, err = detectCoffeeStopFact(ctx, latest.Type, points, s.overpass)
						if err != nil {
							log.Printf("strava coffee stop detection failed (using cached coffee fact): %v", err)
						}
					}
				}
			} else {
				rideFact = rideSegmentFact{}
				coffeeFact = coffeeStopFact{}
				routeFact = routeHighlightFact{}
			}
		}
	} else if s.ingestor != nil {
		log.Printf("strava client unavailable for user %d (using cached description): %v", activity.UserID, clientErr)
	}

	var descPtr *string
	newDesc, descChanged := applyWeirdStatsDescription(baseDescription, filterWeirdStatsSnapshot(statsSnapshot, factSettings), rideFact, coffeeFact, routeFact, roadFact)
	if descChanged {
		descPtr = &newDesc
	}

	var hidePtr *bool
	if hide && !baseHideFromHome {
		val := true
		hidePtr = &val
	}

	cachePoints, cacheErr := s.store.LoadActivityPoints(ctx, activityID)
	if cacheErr != nil {
		log.Printf("activity points load failed (skipping detected facts cache): %v", cacheErr)
	} else {
		cacheStops, err := s.store.LoadActivityStops(ctx, activityID)
		if err != nil {
			log.Printf("activity stops load failed (skipping detected facts cache): %v", err)
		} else {
			s.updateActivityDetectedFactsCache(ctx, activity, cachePoints, cacheStops, rideFact, coffeeFact, routeFact, roadFact)
		}
	}

	if descPtr == nil && hidePtr == nil {
		return nil
	}
	if clientErr != nil {
		return fmt.Errorf("strava client not configured: %w", clientErr)
	}

	if _, err := client.UpdateActivity(ctx, activityID, strava.UpdateActivityRequest{
		Description:  descPtr,
		HideFromHome: hidePtr,
	}); err != nil {
		return err
	}

	descToStore := baseDescription
	if descPtr != nil {
		descToStore = *descPtr
	}
	if err := s.store.UpdateActivityDescriptionAndHideFromHome(ctx, activityID, descToStore, hidePtr); err != nil {
		log.Printf("local update failed: %v", err)
	}
	return nil
}

func totalStopSeconds(stops []StopView) int {
	total := 0
	for _, s := range stops {
		total += s.DurationSeconds
	}
	return total
}

func buildStopViews(storedStops []storage.ActivityStop) []StopView {
	stopViews := make([]StopView, 0, len(storedStops))
	for _, stop := range storedStops {
		stopViews = append(stopViews, StopView{
			Lat:             stop.Lat,
			Lon:             stop.Lon,
			StartSeconds:    stop.StartSeconds,
			Duration:        formatDuration(stop.DurationSeconds),
			DurationSeconds: stop.DurationSeconds,
			HasTrafficLight: stop.HasTrafficLight,
			HasRoadCrossing: stop.HasRoadCrossing,
			CrossingRoad:    stop.CrossingRoad,
		})
	}
	return stopViews
}

func (s *Server) updateActivityDetectedFactsCache(
	ctx context.Context,
	activity storage.Activity,
	points []gps.Point,
	storedStops []storage.ActivityStop,
	rideFact rideSegmentFact,
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
) {
	if roadFact.Count == 0 && len(storedStops) > 0 {
		roadFact = buildRoadCrossingFact(storedStops)
	}
	if isRideType(activity.Type) && len(points) > 1 {
		if rideFact.DistanceMeters <= 0 {
			rideFact = longestRideSegmentFact(activity.Type, points, s.stopOpts)
		}
		if s.overpass != nil {
			if coffeeFact.Name == "" {
				fact, err := detectCoffeeStopFact(ctx, activity.Type, points, s.overpass)
				if err != nil {
					log.Printf("detected facts cache coffee stop failed for activity %d: %v", activity.ID, err)
				} else {
					coffeeFact = fact
				}
			}
			if len(routeFact.Names) == 0 {
				fact, err := detectRouteHighlightFact(ctx, points, s.overpass)
				if err != nil {
					log.Printf("detected facts cache route highlights failed for activity %d: %v", activity.ID, err)
				} else {
					routeFact = fact
				}
			}
		}
	}

	stopViews := buildStopViews(storedStops)
	if err := s.store.ReplaceActivityFactMetrics(ctx, activity, buildActivityFactMetrics(stopViews, rideFact, roadFact)); err != nil {
		log.Printf("activity fact metrics store failed for activity %d: %v", activity.ID, err)
	}

	detectedFacts := buildActivityMapFacts(stopViews, points, rideFact, coffeeFact, routeFact, roadFact)
	payload, err := json.Marshal(detectedFacts)
	if err != nil {
		log.Printf("detected facts cache marshal failed for activity %d: %v", activity.ID, err)
		return
	}
	if err := s.store.UpsertActivityDetectedFacts(ctx, activity.ID, string(payload), time.Time{}); err != nil {
		log.Printf("detected facts cache store failed for activity %d: %v", activity.ID, err)
	}
}

func (s *Server) evaluateHideRules(ctx context.Context, activity storage.Activity) (bool, stats.StopStats, error) {
	statsSnapshot, err := s.loadStatsSnapshot(ctx, activity.ID)
	if err != nil {
		return false, stats.StopStats{}, err
	}
	ruleRows, err := s.store.ListHideRules(ctx, activity.UserID)
	if err != nil {
		return false, stats.StopStats{}, err
	}

	reg := rules.DefaultRegistry()
	startUnix := int64(0)
	if !activity.StartTime.IsZero() {
		startUnix = activity.StartTime.Unix()
	}
	ctxData := rules.Context{
		Activity: rules.ActivitySource{
			ID:          activity.ID,
			Type:        activity.Type,
			Name:        activity.Name,
			StartUnix:   startUnix,
			DistanceM:   activity.Distance,
			MovingTimeS: activity.MovingTime,
		},
		Stats: rules.StatsSource{
			StopCount:             statsSnapshot.StopCount,
			StopTotalSeconds:      statsSnapshot.StopTotalSeconds,
			TrafficLightStopCount: statsSnapshot.TrafficLightStopCount,
			RoadCrossingCount:     statsSnapshot.RoadCrossingCount,
		},
	}

	hide := false
	for _, ruleRow := range ruleRows {
		if !ruleRow.Enabled {
			continue
		}
		ruleDef, err := rules.ParseRuleJSON(ruleRow.Condition)
		if err != nil {
			continue
		}
		if err := rules.ValidateRule(ruleDef, reg); err != nil {
			continue
		}
		matched, shouldHide, err := rules.Evaluate(ruleDef, reg, ctxData, ruleRow.ID)
		if err != nil {
			continue
		}
		if matched && shouldHide {
			hide = true
			break
		}
	}

	return hide, statsSnapshot, nil
}

func (s *Server) loadStatsSnapshot(ctx context.Context, activityID int64) (stats.StopStats, error) {
	statsSnapshot, err := s.store.GetActivityStats(ctx, activityID)
	if err == nil {
		return statsSnapshot, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return stats.StopStats{}, err
	}

	stops, err := s.store.LoadActivityStops(ctx, activityID)
	if err != nil {
		return stats.StopStats{}, err
	}
	return stopStatsFromStops(stops), nil
}

func stopStatsFromStops(stops []storage.ActivityStop) stats.StopStats {
	snapshot := stats.StopStats{
		StopCount: len(stops),
	}
	for _, stop := range stops {
		snapshot.StopTotalSeconds += stop.DurationSeconds
		if stop.HasTrafficLight {
			snapshot.TrafficLightStopCount++
		}
		if stop.HasRoadCrossing {
			snapshot.RoadCrossingCount++
		}
	}
	return snapshot
}

const weirdStatsPrefix = "Weirdstats:"
const weirdstatsTag = "#weirdstats"

func applyWeirdStatsDescription(existing string, statsSnapshot stats.StopStats, rideFact rideSegmentFact, coffeeFact coffeeStopFact, routeFact routeHighlightFact, roadFact roadCrossingFact) (string, bool) {
	line := buildWeirdStatsLine(statsSnapshot, rideFact, coffeeFact, routeFact, roadFact)
	normalized := strings.ReplaceAll(existing, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	filtered := make([]string, 0, len(lines))
	hadManagedLine := false
	for _, l := range lines {
		if isWeirdstatsManagedLine(l) {
			hadManagedLine = true
			continue
		}
		filtered = append(filtered, l)
	}

	base := strings.TrimRight(strings.Join(filtered, "\n"), "\n")
	if line == "" {
		if !hadManagedLine {
			return existing, false
		}
		return base, base != existing
	}

	updated := line
	if strings.TrimSpace(base) != "" {
		updated = base + "\n\n" + line
	}
	updated = appendWeirdstatsTag(updated)

	return updated, updated != existing
}

func buildWeirdStatsLine(statsSnapshot stats.StopStats, rideFact rideSegmentFact, coffeeFact coffeeStopFact, routeFact routeHighlightFact, roadFact roadCrossingFact) string {
	ridePart := buildRideSegmentPart(rideFact)
	coffeePart := buildCoffeeStopPart(coffeeFact)
	routePart := buildRouteHighlightPart(routeFact)
	roadCount := roadFact.Count
	if roadCount <= 0 {
		roadCount = statsSnapshot.RoadCrossingCount
	}
	roadPart := buildRoadCrossingPartWithCount(roadCount, roadFact.Roads)
	if statsSnapshot.StopCount == 0 && statsSnapshot.TrafficLightStopCount == 0 && ridePart == "" && coffeePart == "" && routePart == "" && roadPart == "" {
		return ""
	}
	parts := make([]string, 0, 6)
	if ridePart != "" {
		parts = append(parts, ridePart)
	}
	if coffeePart != "" {
		parts = append(parts, coffeePart)
	}
	if routePart != "" {
		parts = append(parts, routePart)
	}
	if roadPart != "" {
		parts = append(parts, roadPart)
	}
	if statsSnapshot.StopCount > 0 {
		part := fmt.Sprintf("%d stops", statsSnapshot.StopCount)
		if statsSnapshot.StopTotalSeconds > 0 {
			part += fmt.Sprintf(" (%s total)", formatDuration(statsSnapshot.StopTotalSeconds))
		}
		parts = append(parts, part)
	}
	if statsSnapshot.TrafficLightStopCount > 0 {
		parts = append(parts, fmt.Sprintf("%d at lights", statsSnapshot.TrafficLightStopCount))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func buildRideSegmentPart(fact rideSegmentFact) string {
	if fact.DistanceMeters <= 0 || fact.AvgSpeedMPS <= 0 {
		return ""
	}
	parts := []string{formatCompactNumber(fact.DistanceMeters/1000, 1) + "km"}
	if fact.AvgPower > 0 {
		parts = append(parts, formatCompactNumber(fact.AvgPower, 0)+"w")
	}
	parts = append(parts, formatCompactNumber(fact.AvgSpeedMPS*3.6, 1)+"kmh")
	return "Longest uninterrupted segment: " + strings.Join(parts, " - ")
}

func buildCoffeeStopPart(fact coffeeStopFact) string {
	name := strings.TrimSpace(fact.Name)
	if name == "" {
		return ""
	}
	return "Detected Coffee Stop: " + name
}

func buildRouteHighlightPart(fact routeHighlightFact) string {
	names := make([]string, 0, len(fact.Names))
	seen := make(map[string]struct{}, len(fact.Names))
	for _, name := range fact.Names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := normalizeHighlightName(trimmed)
		if key == "" {
			key = trimmed
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, trimmed)
		if len(names) == routeHighlightMaxCount {
			break
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Route highlights: " + strings.Join(names, ", ")
}

func buildRoadCrossingPart(fact roadCrossingFact) string {
	return buildRoadCrossingPartWithCount(fact.Count, fact.Roads)
}

func buildRoadCrossingPartWithCount(count int, roads []string) string {
	if count <= 0 {
		return ""
	}
	roads = uniqueCrossingRoadNames(roads)
	if count == 1 {
		if len(roads) > 0 {
			return "Road crossing: " + roads[0]
		}
		return "1 road crossing"
	}
	if len(roads) > 0 {
		return fmt.Sprintf("%d road crossings: %s", count, strings.Join(roads, ", "))
	}
	return fmt.Sprintf("%d road crossings", count)
}

func buildRoadCrossingFact(stops []storage.ActivityStop) roadCrossingFact {
	fact := roadCrossingFact{}
	for _, stop := range stops {
		if !stop.HasRoadCrossing {
			continue
		}
		fact.Count++
		fact.Locations = append(fact.Locations, roadCrossingLocation{
			Lat:  stop.Lat,
			Lon:  stop.Lon,
			Road: strings.TrimSpace(stop.CrossingRoad),
		})
		if name := strings.TrimSpace(stop.CrossingRoad); name != "" {
			fact.Roads = append(fact.Roads, name)
		}
	}
	fact.Roads = uniqueCrossingRoadNames(fact.Roads)
	if len(fact.Roads) > roadCrossingFactMaxNames {
		fact.Roads = fact.Roads[:roadCrossingFactMaxNames]
	}
	return fact
}

func uniqueCrossingRoadNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func appendWeirdstatsTag(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, weirdstatsTag))
	if trimmed == "" {
		return weirdstatsTag
	}
	return trimmed + " " + weirdstatsTag
}

func splitStoredActivityDescription(description string) (string, int) {
	normalized := strings.ReplaceAll(description, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	baseLines := make([]string, 0, len(lines))
	detectedFactCount := 0
	for _, line := range lines {
		if isWeirdstatsManagedLine(line) {
			detectedFactCount += countDetectedFactsInLine(line)
			continue
		}
		baseLines = append(baseLines, line)
	}
	return strings.TrimSpace(strings.Join(baseLines, "\n")), detectedFactCount
}

func countDetectedFactsInLine(line string) int {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.EqualFold(trimmed, weirdstatsTag) {
		return 0
	}
	if strings.HasPrefix(trimmed, weirdStatsPrefix) {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, weirdStatsPrefix))
	}
	trimmed = strings.TrimSpace(strings.ReplaceAll(trimmed, weirdstatsTag, ""))
	if trimmed == "" {
		return 0
	}
	parts := strings.Split(trimmed, " · ")
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

func isWeirdstatsManagedLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, weirdStatsPrefix) || strings.EqualFold(trimmed, weirdstatsTag) {
		return true
	}
	if !strings.Contains(trimmed, weirdstatsTag) {
		return false
	}
	return strings.Contains(trimmed, "stops") ||
		strings.Contains(trimmed, "at lights") ||
		strings.Contains(trimmed, "Longest uninterrupted segment:") ||
		strings.Contains(trimmed, "Detected Coffee Stop:") ||
		strings.Contains(trimmed, "Route highlights:") ||
		strings.Contains(trimmed, "Road crossing:") ||
		strings.Contains(trimmed, "road crossings")
}
