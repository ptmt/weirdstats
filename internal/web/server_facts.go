package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/strava"
)

type rideSegmentFact struct {
	DistanceMeters float64
	AvgPower       float64
	AvgSpeedMPS    float64
	StartIndex     int
	EndIndex       int
	StartLat       float64
	StartLon       float64
	EndLat         float64
	EndLon         float64
}

type coffeeStopFact struct {
	Name        string
	Lat         float64
	Lon         float64
	HasLocation bool
}

type routeHighlightLocation struct {
	Name string
	Lat  float64
	Lon  float64
}

type routeHighlightFact struct {
	Names     []string
	Locations []routeHighlightLocation
}

type roadCrossingLocation struct {
	Lat  float64
	Lon  float64
	Road string
}

type roadCrossingFact struct {
	Count     int
	Roads     []string
	Locations []roadCrossingLocation
}

func buildPointsFromStreams(start time.Time, streams strava.StreamSet) []gps.Point {
	if len(streams.LatLng) == 0 || len(streams.TimeOffsetsSec) == 0 {
		return nil
	}
	if len(streams.LatLng) != len(streams.TimeOffsetsSec) {
		return nil
	}

	points := make([]gps.Point, 0, len(streams.LatLng))
	for idx, coord := range streams.LatLng {
		point := gps.Point{
			Lat:  coord[0],
			Lon:  coord[1],
			Time: start.Add(time.Duration(streams.TimeOffsetsSec[idx]) * time.Second),
		}
		if idx < len(streams.VelocitySmooth) {
			point.Speed = streams.VelocitySmooth[idx]
		}
		if idx < len(streams.Watts) {
			point.Power = streams.Watts[idx]
			point.HasPower = true
		}
		if idx < len(streams.GradeSmooth) {
			point.Grade = streams.GradeSmooth[idx]
			point.HasGrade = true
		}
		points = append(points, point)
	}
	return points
}

const (
	longestRideSegmentMinSpeedKPH = 15.0
	longestRideSegmentMinSpeedMPS = longestRideSegmentMinSpeedKPH / 3.6
	longestRideSegmentMinSlowTime = 5 * time.Second
	coffeeStopMinDuration         = 3 * time.Minute
	coffeeStopSpeedThresholdMPS   = 0.5
	coffeeStopSearchRadiusMeters  = 45
	routeHighlightMaxDistanceM    = 200.0
	routeHighlightBBoxPaddingM    = 200.0
	routeHighlightMinScore        = 25.0
	routeHighlightMaxCount        = 2
	roadCrossingFactMaxNames      = 2
)

func longestRideSegmentFact(activityType string, points []gps.Point, _ gps.StopOptions) rideSegmentFact {
	if !isRideType(activityType) || len(points) < 2 {
		return rideSegmentFact{}
	}

	windows := buildRideSegmentWindows(points, longestRideSegmentMinSpeedMPS, longestRideSegmentMinSlowTime)
	best := rideSegmentFact{}
	for _, window := range windows {
		fact := rideSegmentFactForWindow(points, window.start, window.end, longestRideSegmentMinSpeedMPS)
		if fact.DistanceMeters > best.DistanceMeters {
			best = fact
		}
	}
	return best
}

type rideSegmentWindow struct {
	start time.Time
	end   time.Time
}

type pauseWindow struct {
	startIdx int
	endIdx   int
	start    time.Time
	end      time.Time
	duration time.Duration
}

func buildRideSegmentWindows(points []gps.Point, minSpeedMPS float64, minSlowTime time.Duration) []rideSegmentWindow {
	if len(points) == 0 {
		return nil
	}

	lastPointTime := points[len(points)-1].Time
	windows := make([]rideSegmentWindow, 0, 4)
	start := points[0].Time

	var (
		inSlow   bool
		slowFrom time.Time
	)
	for _, point := range points {
		if point.Speed < minSpeedMPS {
			if !inSlow {
				inSlow = true
				slowFrom = point.Time
			}
			continue
		}

		if !inSlow {
			continue
		}

		if point.Time.Sub(slowFrom) >= minSlowTime {
			if slowFrom.After(start) {
				windows = append(windows, rideSegmentWindow{start: start, end: slowFrom})
			}
			start = point.Time
		}
		inSlow = false
	}

	if inSlow && lastPointTime.Sub(slowFrom) >= minSlowTime {
		if slowFrom.After(start) {
			windows = append(windows, rideSegmentWindow{start: start, end: slowFrom})
		}
		return windows
	}

	if lastPointTime.After(start) {
		windows = append(windows, rideSegmentWindow{start: start, end: lastPointTime})
	}
	if len(windows) == 0 && lastPointTime.After(points[0].Time) {
		windows = append(windows, rideSegmentWindow{start: points[0].Time, end: lastPointTime})
	}
	return windows
}

func rideSegmentFactForWindow(points []gps.Point, start, end time.Time, speedThreshold float64) rideSegmentFact {
	if !end.After(start) {
		return rideSegmentFact{}
	}

	var (
		prev       gps.Point
		havePrev   bool
		distanceM  float64
		speedTotal float64
		speedCount int
		powerTotal float64
		powerCount int
	)

	firstIdx := -1
	lastIdx := -1
	for idx, point := range points {
		if point.Time.Before(start) || point.Time.After(end) {
			continue
		}
		if firstIdx == -1 {
			firstIdx = idx
		}
		lastIdx = idx
		if havePrev {
			distanceM += haversineMeters(prev.Lat, prev.Lon, point.Lat, point.Lon)
		}
		prev = point
		havePrev = true

		if point.Speed <= speedThreshold {
			continue
		}
		speedTotal += point.Speed
		speedCount++
		if point.Power > 0 {
			powerTotal += point.Power
			powerCount++
		}
	}

	if distanceM <= 0 || speedCount == 0 {
		return rideSegmentFact{}
	}

	fact := rideSegmentFact{
		DistanceMeters: distanceM,
		AvgSpeedMPS:    speedTotal / float64(speedCount),
		StartIndex:     firstIdx,
		EndIndex:       lastIdx,
		StartLat:       points[firstIdx].Lat,
		StartLon:       points[firstIdx].Lon,
		EndLat:         points[lastIdx].Lat,
		EndLon:         points[lastIdx].Lon,
	}
	if powerCount > 0 {
		fact.AvgPower = powerTotal / float64(powerCount)
	}
	return fact
}

func detectRouteHighlightFact(ctx context.Context, points []gps.Point, overpass *maps.OverpassClient) (routeHighlightFact, error) {
	if len(points) < 2 || overpass == nil {
		return routeHighlightFact{}, nil
	}

	bbox, ok := routeBBox(points, routeHighlightBBoxPaddingM)
	if !ok {
		return routeHighlightFact{}, nil
	}

	pois, err := overpass.FetchLandmarkPOIs(ctx, bbox)
	if err != nil {
		return routeHighlightFact{}, err
	}

	candidates := buildRouteHighlightCandidates(points, pois, routeHighlightMaxDistanceM)
	if len(candidates) == 0 {
		return routeHighlightFact{}, nil
	}

	names := make([]string, 0, routeHighlightMaxCount)
	locations := make([]routeHighlightLocation, 0, routeHighlightMaxCount)
	for _, candidate := range candidates {
		names = append(names, candidate.name)
		locations = append(locations, routeHighlightLocation{
			Name: candidate.name,
			Lat:  candidate.lat,
			Lon:  candidate.lon,
		})
		if len(names) == routeHighlightMaxCount {
			break
		}
	}
	if len(names) == 0 {
		return routeHighlightFact{}, nil
	}
	return routeHighlightFact{
		Names:     names,
		Locations: locations,
	}, nil
}

type routeHighlightCandidate struct {
	name           string
	score          float64
	distanceMeters float64
	lat            float64
	lon            float64
}

func buildRouteHighlightCandidates(points []gps.Point, pois []maps.POI, maxDistanceMeters float64) []routeHighlightCandidate {
	bestByName := make(map[string]routeHighlightCandidate)
	for _, poi := range pois {
		candidate, ok := routeHighlightCandidateForPOI(points, poi, maxDistanceMeters)
		if !ok {
			continue
		}
		key := normalizeHighlightName(candidate.name)
		if current, exists := bestByName[key]; !exists || routeHighlightCandidateBetter(candidate, current) {
			bestByName[key] = candidate
		}
	}

	candidates := make([]routeHighlightCandidate, 0, len(bestByName))
	for _, candidate := range bestByName {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return routeHighlightCandidateBetter(candidates[i], candidates[j])
	})
	return candidates
}

func routeHighlightCandidateForPOI(points []gps.Point, poi maps.POI, maxDistanceMeters float64) (routeHighlightCandidate, bool) {
	name := strings.TrimSpace(poi.Name)
	if name == "" {
		return routeHighlightCandidate{}, false
	}

	distanceMeters := minDistanceToRouteMeters(poi.Lat, poi.Lon, points)
	if distanceMeters > maxDistanceMeters {
		return routeHighlightCandidate{}, false
	}

	score := routeHighlightScore(poi.Tags, distanceMeters, maxDistanceMeters)
	if score < routeHighlightMinScore {
		return routeHighlightCandidate{}, false
	}

	return routeHighlightCandidate{
		name:           name,
		score:          score,
		distanceMeters: distanceMeters,
		lat:            poi.Lat,
		lon:            poi.Lon,
	}, true
}

func routeHighlightCandidateBetter(candidate routeHighlightCandidate, current routeHighlightCandidate) bool {
	if candidate.score != current.score {
		return candidate.score > current.score
	}
	if candidate.distanceMeters != current.distanceMeters {
		return candidate.distanceMeters < current.distanceMeters
	}
	return candidate.name < current.name
}

func routeHighlightScore(tags map[string]string, distanceMeters float64, maxDistanceMeters float64) float64 {
	score := 0.0
	if strings.TrimSpace(tags["wikidata"]) != "" {
		score += 40
	}
	if strings.TrimSpace(tags["wikipedia"]) != "" {
		score += 35
	}
	if strings.TrimSpace(tags["heritage"]) != "" {
		score += 30
	}

	switch tags["tourism"] {
	case "attraction":
		score += 25
	case "museum":
		score += 20
	case "viewpoint":
		score += 16
	case "artwork":
		score += 14
	}

	switch tags["historic"] {
	case "castle":
		score += 24
	case "monument":
		score += 18
	case "archaeological_site":
		score += 18
	case "ruins":
		score += 16
	case "memorial":
		score += 14
	}

	switch tags["building"] {
	case "cathedral":
		score += 16
	case "church":
		score += 12
	}

	if tags["amenity"] == "place_of_worship" {
		score += 8
	}
	if maxDistanceMeters > 0 && distanceMeters < maxDistanceMeters {
		score += (maxDistanceMeters - distanceMeters) / 10
	}
	return score
}

func routeBBox(points []gps.Point, paddingMeters float64) (maps.BBox, bool) {
	if len(points) == 0 {
		return maps.BBox{}, false
	}

	minLat, maxLat := points[0].Lat, points[0].Lat
	minLon, maxLon := points[0].Lon, points[0].Lon
	latTotal := 0.0
	for _, point := range points {
		if point.Lat < minLat {
			minLat = point.Lat
		}
		if point.Lat > maxLat {
			maxLat = point.Lat
		}
		if point.Lon < minLon {
			minLon = point.Lon
		}
		if point.Lon > maxLon {
			maxLon = point.Lon
		}
		latTotal += point.Lat
	}

	avgLat := latTotal / float64(len(points))
	latPadding := paddingMeters / 111320.0
	lonScale := math.Cos(avgLat * math.Pi / 180)
	if math.Abs(lonScale) < 1e-6 {
		lonScale = 1e-6
	}
	lonPadding := paddingMeters / (111320.0 * lonScale)
	return maps.BBox{
		South: minLat - latPadding,
		West:  minLon - lonPadding,
		North: maxLat + latPadding,
		East:  maxLon + lonPadding,
	}, true
}

func minDistanceToRouteMeters(lat, lon float64, points []gps.Point) float64 {
	if len(points) == 0 {
		return math.Inf(1)
	}
	if len(points) == 1 {
		return haversineMeters(lat, lon, points[0].Lat, points[0].Lon)
	}

	const earthRadius = 6371000.0
	latRad := lat * math.Pi / 180
	lonScale := math.Cos(latRad)
	if math.Abs(lonScale) < 1e-6 {
		lonScale = 1e-6
	}

	project := func(point gps.Point) (float64, float64) {
		x := (point.Lon - lon) * math.Pi / 180 * earthRadius * lonScale
		y := (point.Lat - lat) * math.Pi / 180 * earthRadius
		return x, y
	}

	prevX, prevY := project(points[0])
	best := math.Hypot(prevX, prevY)
	for _, point := range points[1:] {
		x, y := project(point)
		distance := pointToSegmentDistanceMeters(0, 0, prevX, prevY, x, y)
		if distance < best {
			best = distance
		}
		prevX, prevY = x, y
	}
	return best
}

func pointToSegmentDistanceMeters(px, py, x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x1, py-y1)
	}

	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	projX := x1 + (t * dx)
	projY := y1 + (t * dy)
	return math.Hypot(px-projX, py-projY)
}

func normalizeHighlightName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

func detectCoffeeStopFact(ctx context.Context, activityType string, points []gps.Point, overpass *maps.OverpassClient) (coffeeStopFact, error) {
	if !isRideType(activityType) || len(points) == 0 || overpass == nil {
		return coffeeStopFact{}, nil
	}
	if !hasMovingPoints(points, coffeeStopSpeedThresholdMPS) {
		return coffeeStopFact{}, nil
	}

	windows := buildPauseWindows(points, coffeeStopSpeedThresholdMPS, coffeeStopMinDuration)
	best := coffeeStopCandidate{}
	for _, window := range windows {
		lat, lon, ok := pauseCentroid(points, window.startIdx, window.endIdx)
		if !ok {
			continue
		}

		pois, err := overpass.FetchNearbyFoodPOIs(ctx, lat, lon, coffeeStopSearchRadiusMeters)
		if err != nil {
			return coffeeStopFact{}, err
		}

		poi, distanceMeters, ok := selectCoffeePOI(lat, lon, pois)
		if !ok {
			continue
		}

		candidate := coffeeStopCandidate{
			name:           coffeeStopDisplayName(poi),
			duration:       window.duration,
			distanceMeters: distanceMeters,
			pauseStart:     window.start,
			isCafe:         poi.Type == maps.FeatureCafe,
			hasName:        strings.TrimSpace(poi.Name) != "",
			lat:            poi.Lat,
			lon:            poi.Lon,
			valid:          true,
		}
		if candidate.betterThan(best) {
			best = candidate
		}
	}

	if !best.valid {
		return coffeeStopFact{}, nil
	}
	return coffeeStopFact{
		Name:        best.name,
		Lat:         best.lat,
		Lon:         best.lon,
		HasLocation: true,
	}, nil
}

type coffeeStopCandidate struct {
	name           string
	duration       time.Duration
	distanceMeters float64
	pauseStart     time.Time
	isCafe         bool
	hasName        bool
	lat            float64
	lon            float64
	valid          bool
}

func (c coffeeStopCandidate) betterThan(other coffeeStopCandidate) bool {
	if !other.valid {
		return c.valid
	}
	if c.isCafe != other.isCafe {
		return c.isCafe
	}
	if c.hasName != other.hasName {
		return c.hasName
	}
	if c.duration != other.duration {
		return c.duration > other.duration
	}
	if c.distanceMeters != other.distanceMeters {
		return c.distanceMeters < other.distanceMeters
	}
	return c.pauseStart.Before(other.pauseStart)
}

func buildPauseWindows(points []gps.Point, speedThreshold float64, minDuration time.Duration) []pauseWindow {
	if len(points) == 0 {
		return nil
	}

	windows := make([]pauseWindow, 0, 2)
	inPause := false
	startIdx := 0
	lastSlowIdx := 0

	for idx, point := range points {
		if point.Speed <= speedThreshold {
			if !inPause {
				inPause = true
				startIdx = idx
			}
			lastSlowIdx = idx
			continue
		}

		if !inPause {
			continue
		}

		duration := points[lastSlowIdx].Time.Sub(points[startIdx].Time)
		if duration >= minDuration {
			windows = append(windows, pauseWindow{
				startIdx: startIdx,
				endIdx:   lastSlowIdx,
				start:    points[startIdx].Time,
				end:      points[lastSlowIdx].Time,
				duration: duration,
			})
		}
		inPause = false
	}

	if !inPause {
		return windows
	}

	duration := points[lastSlowIdx].Time.Sub(points[startIdx].Time)
	if duration >= minDuration {
		windows = append(windows, pauseWindow{
			startIdx: startIdx,
			endIdx:   lastSlowIdx,
			start:    points[startIdx].Time,
			end:      points[lastSlowIdx].Time,
			duration: duration,
		})
	}
	return windows
}

func pauseCentroid(points []gps.Point, startIdx, endIdx int) (float64, float64, bool) {
	if startIdx < 0 || endIdx >= len(points) || startIdx > endIdx {
		return 0, 0, false
	}

	var (
		latTotal float64
		lonTotal float64
		count    int
	)
	for idx := startIdx; idx <= endIdx; idx++ {
		latTotal += points[idx].Lat
		lonTotal += points[idx].Lon
		count++
	}
	if count == 0 {
		return 0, 0, false
	}
	return latTotal / float64(count), lonTotal / float64(count), true
}

func hasMovingPoints(points []gps.Point, speedThreshold float64) bool {
	for _, point := range points {
		if point.Speed > speedThreshold {
			return true
		}
	}
	return false
}

func selectCoffeePOI(lat, lon float64, pois []maps.POI) (maps.POI, float64, bool) {
	best := maps.POI{}
	bestDistance := 0.0
	bestFound := false
	for _, poi := range pois {
		if poi.Type != maps.FeatureCafe && poi.Type != maps.FeatureRestaurant {
			continue
		}

		distance := haversineMeters(lat, lon, poi.Lat, poi.Lon)
		if !bestFound {
			best = poi
			bestDistance = distance
			bestFound = true
			continue
		}

		if coffeePOIBetter(poi, distance, best, bestDistance) {
			best = poi
			bestDistance = distance
		}
	}
	return best, bestDistance, bestFound
}

func coffeePOIBetter(candidate maps.POI, candidateDistance float64, current maps.POI, currentDistance float64) bool {
	candidateIsCafe := candidate.Type == maps.FeatureCafe
	currentIsCafe := current.Type == maps.FeatureCafe
	if candidateIsCafe != currentIsCafe {
		return candidateIsCafe
	}

	candidateHasName := strings.TrimSpace(candidate.Name) != ""
	currentHasName := strings.TrimSpace(current.Name) != ""
	if candidateHasName != currentHasName {
		return candidateHasName
	}

	if candidateDistance != currentDistance {
		return candidateDistance < currentDistance
	}
	return strings.TrimSpace(candidate.Name) < strings.TrimSpace(current.Name)
}

func coffeeStopDisplayName(poi maps.POI) string {
	if name := strings.TrimSpace(poi.Name); name != "" {
		return name
	}
	switch poi.Type {
	case maps.FeatureCafe:
		return "Unnamed cafe"
	case maps.FeatureRestaurant:
		return "Unnamed restaurant"
	default:
		return "Unnamed stop"
	}
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000.0
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}

func formatCompactNumber(value float64, precision int) string {
	scale := math.Pow(10, float64(precision))
	rounded := math.Round(value*scale) / scale
	text := fmt.Sprintf("%.*f", precision, rounded)
	if precision > 0 {
		text = strings.TrimSuffix(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	return text
}

func buildActivityMapFacts(
	stopViews []StopView,
	points []gps.Point,
	rideFact rideSegmentFact,
	speedFacts []speedMilestoneFact,
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
) []ActivityMapFactView {
	facts := make([]ActivityMapFactView, 0, 10)

	if summary := trimFactPrefix(buildRideSegmentPart(rideFact), "Longest segment: "); summary != "" {
		fact := ActivityMapFactView{
			ID:      weirdStatsFactLongestSegment,
			Kind:    "segment",
			Title:   "Longest segment",
			Summary: summary,
			Color:   "#22c55e",
			Points: []ActivityFactPoint{
				{Lat: rideFact.StartLat, Lon: rideFact.StartLon, Label: "Segment start"},
				{Lat: rideFact.EndLat, Lon: rideFact.EndLon, Label: "Segment end"},
			},
			Path: rideSegmentPathPoints(points, rideFact),
		}
		facts = append(facts, fact)
	}

	for _, speedFact := range speedFacts {
		if summary := speedMilestoneSummary(speedFact); summary != "" {
			facts = append(facts, ActivityMapFactView{
				ID:      speedFact.FactID,
				Kind:    "segment",
				Title:   speedFact.Label,
				Summary: summary,
				Color:   speedFact.Color,
				Points: []ActivityFactPoint{
					{Lat: speedFact.StartLat, Lon: speedFact.StartLon, Label: "Start"},
					{Lat: speedFact.EndLat, Lon: speedFact.EndLon, Label: "Finish"},
				},
				Path: speedMilestonePathPoints(points, speedFact),
			})
		}
	}

	if summary := trimFactPrefix(buildCoffeeStopPart(coffeeFact), "Detected Coffee Stop: "); summary != "" && coffeeFact.HasLocation {
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactCoffeeStop,
			Kind:    "point",
			Title:   "Coffee stop",
			Summary: summary,
			Color:   "#f59e0b",
			Points: []ActivityFactPoint{
				{Lat: coffeeFact.Lat, Lon: coffeeFact.Lon, Label: summary},
			},
		})
	}

	if summary := trimFactPrefix(buildRouteHighlightPart(routeFact), "Route highlights: "); summary != "" && len(routeFact.Locations) > 0 {
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactRouteHighlights,
			Kind:    "collection",
			Title:   "Route highlights",
			Summary: summary,
			Color:   "#06b6d4",
			Points:  routeHighlightFactPoints(routeFact.Locations),
		})
	}

	if summary := buildRoadCrossingPart(roadFact); summary != "" && len(roadFact.Locations) > 0 {
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactRoadCrossings,
			Kind:    "collection",
			Title:   "Road crossings",
			Summary: summary,
			Color:   "#3b82f6",
			Points:  roadCrossingFactPoints(roadFact.Locations),
		})
	}

	if len(stopViews) > 0 {
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactStopSummary,
			Kind:    "collection",
			Title:   "Stop summary",
			Summary: stopSummaryFactSummary(stopViews),
			Color:   "#ec4899",
			Points:  stopFactPoints(stopViews),
		})
	}

	lightStops := filterStopViews(stopViews, func(stop StopView) bool {
		return stop.HasTrafficLight
	})
	if len(lightStops) > 0 {
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactTrafficLightStops,
			Kind:    "collection",
			Title:   "Traffic-light stops",
			Summary: trafficLightStopsFactSummary(len(lightStops)),
			Color:   "#ef4444",
			Points:  stopFactPoints(lightStops),
		})
	}

	return facts
}

func trimFactPrefix(part, prefix string) string {
	if part == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(part, prefix))
}

func rideSegmentPathPoints(points []gps.Point, fact rideSegmentFact) []routePreviewPoint {
	return pathPointsBetweenIndices(points, fact.StartIndex, fact.EndIndex)
}

func pathPointsBetweenIndices(points []gps.Point, startIndex, endIndex int) []routePreviewPoint {
	if startIndex < 0 || endIndex >= len(points) || startIndex >= endIndex {
		return nil
	}
	path := make([]routePreviewPoint, 0, endIndex-startIndex+1)
	for _, point := range points[startIndex : endIndex+1] {
		path = append(path, routePreviewPoint{Lat: point.Lat, Lon: point.Lon})
	}
	return path
}

func stopFactPoints(stops []StopView) []ActivityFactPoint {
	points := make([]ActivityFactPoint, 0, len(stops))
	for _, stop := range stops {
		label := stop.Duration
		if stop.HasTrafficLight {
			label += " · traffic light"
		} else if stop.HasRoadCrossing {
			label += " · road crossing"
		}
		if stop.CrossingRoad != "" {
			label += " · " + stop.CrossingRoad
		}
		points = append(points, ActivityFactPoint{
			Lat:   stop.Lat,
			Lon:   stop.Lon,
			Label: label,
		})
	}
	return points
}

func roadCrossingFactPoints(locations []roadCrossingLocation) []ActivityFactPoint {
	points := make([]ActivityFactPoint, 0, len(locations))
	for _, location := range locations {
		label := "Road crossing"
		if location.Road != "" {
			label = location.Road
		}
		points = append(points, ActivityFactPoint{
			Lat:   location.Lat,
			Lon:   location.Lon,
			Label: label,
		})
	}
	return points
}

func routeHighlightFactPoints(locations []routeHighlightLocation) []ActivityFactPoint {
	points := make([]ActivityFactPoint, 0, len(locations))
	for _, location := range locations {
		points = append(points, ActivityFactPoint{
			Lat:   location.Lat,
			Lon:   location.Lon,
			Label: location.Name,
		})
	}
	return points
}

func filterStopViews(stops []StopView, keep func(StopView) bool) []StopView {
	filtered := make([]StopView, 0, len(stops))
	for _, stop := range stops {
		if keep(stop) {
			filtered = append(filtered, stop)
		}
	}
	return filtered
}

func isRideType(activityType string) bool {
	return strings.Contains(strings.ToLower(activityType), "ride")
}

func (s *Server) redirectBack(w http.ResponseWriter, r *http.Request, activityID int64, msg string) {
	redirectURL := r.Header.Get("Referer")
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("/activity/%d", activityID)
	}
	if msg != "" {
		sep := "?"
		if strings.Contains(redirectURL, "?") {
			sep = "&"
		}
		redirectURL = redirectURL + sep + "msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func countLightStops(stops []StopView) int {
	total := 0
	for _, s := range stops {
		if s.HasTrafficLight {
			total++
		}
	}
	return total
}

func countRoadCrossings(stops []StopView) int {
	total := 0
	for _, s := range stops {
		if s.HasRoadCrossing {
			total++
		}
	}
	return total
}
