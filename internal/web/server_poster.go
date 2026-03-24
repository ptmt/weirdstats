package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

const (
	posterMapWidth        = 1000.0
	posterMapHeight       = 1120.0
	posterMapPadding      = 58.0
	posterFactMarkerLimit = 6
)

type posterPoint struct {
	X float64
	Y float64
}

type posterFactView struct {
	Index       int
	Title       string
	Summary     string
	Color       string
	OverlayPath string
	Points      []posterPoint
	MarkerX     float64
	MarkerY     float64
	HasMarker   bool
}

type posterPageData struct {
	Title        string
	ActivityID   int64
	ActivityName string
	ActivityType string
	ActivityTime string
	Distance     string
	Duration     string
	RoutePath    string
	RouteStartX  float64
	RouteStartY  float64
	RouteEndX    float64
	RouteEndY    float64
	HasRoute     bool
	Facts        []posterFactView
}

type posterProjection struct {
	minLat  float64
	minLon  float64
	scale   float64
	offsetX float64
	offsetY float64
}

func (s *Server) ActivityPoster(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	idStr = strings.TrimSuffix(idStr, "/poster")
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	activity, err := s.store.GetActivityForUser(r.Context(), userID, activityID)
	if err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}

	points, err := s.store.LoadActivityPoints(r.Context(), activityID)
	if err != nil {
		http.Error(w, "failed to load points", http.StatusInternalServerError)
		return
	}
	storedStops, err := s.store.LoadActivityStops(r.Context(), activityID)
	if err != nil {
		http.Error(w, "failed to load stops", http.StatusInternalServerError)
		return
	}

	detectedFacts, err := s.posterFacts(r.Context(), activityID, points, storedStops)
	if err != nil {
		http.Error(w, "failed to load detected facts", http.StatusInternalServerError)
		return
	}

	routePoints := posterRoutePoints(points)
	routePath := ""
	startX := 0.0
	startY := 0.0
	endX := 0.0
	endY := 0.0
	hasRoute := false
	projectedFacts := []posterFactView{}

	if proj, ok := newPosterProjection(routePoints, posterMapWidth, posterMapHeight, posterMapPadding); ok {
		routePath, startX, startY, endX, endY, hasRoute = buildPosterRoutePath(routePoints, proj)
		projectedFacts = buildPosterFactViews(detectedFacts, proj)
	}

	data := posterPageData{
		Title:        activity.Name,
		ActivityID:   activity.ID,
		ActivityName: activity.Name,
		ActivityType: activity.Type,
		ActivityTime: activity.StartTime.Format("Jan 2, 2006 15:04"),
		Distance:     formatDistance(activity.Distance),
		Duration:     formatDuration(activity.MovingTime),
		RoutePath:    routePath,
		RouteStartX:  startX,
		RouteStartY:  startY,
		RouteEndX:    endX,
		RouteEndY:    endY,
		HasRoute:     hasRoute,
		Facts:        projectedFacts,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates["poster"].ExecuteTemplate(w, "poster", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) posterFacts(ctx context.Context, activityID int64, points []gps.Point, storedStops []storage.ActivityStop) ([]ActivityMapFactView, error) {
	rawDetectedFactsJSON, _, err := s.store.GetActivityDetectedFacts(ctx, activityID)
	if err == nil {
		var detectedFacts []ActivityMapFactView
		if strings.TrimSpace(rawDetectedFactsJSON) == "" {
			return detectedFacts, nil
		}
		if err := json.Unmarshal([]byte(rawDetectedFactsJSON), &detectedFacts); err != nil {
			return nil, err
		}
		return detectedFacts, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	stopViews := buildStopViews(storedStops)
	return buildActivityMapFacts(
		stopViews,
		points,
		rideSegmentFact{},
		coffeeStopFact{},
		routeHighlightFact{},
		buildRoadCrossingFact(storedStops),
	), nil
}

func posterRoutePoints(points []gps.Point) []routePreviewPoint {
	out := make([]routePreviewPoint, 0, len(points))
	for _, point := range points {
		out = append(out, routePreviewPoint{Lat: point.Lat, Lon: point.Lon})
	}
	return out
}

func newPosterProjection(points []routePreviewPoint, width, height, padding float64) (posterProjection, bool) {
	if len(points) == 0 {
		return posterProjection{}, false
	}

	minLat, maxLat := points[0].Lat, points[0].Lat
	minLon, maxLon := points[0].Lon, points[0].Lon
	for _, point := range points[1:] {
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
	}

	latRange := maxLat - minLat
	lonRange := maxLon - minLon
	if latRange == 0 {
		latRange = 0.0001
		minLat -= latRange / 2
	}
	if lonRange == 0 {
		lonRange = 0.0001
		minLon -= lonRange / 2
	}

	drawWidth := width - padding*2
	drawHeight := height - padding*2
	scaleX := drawWidth / lonRange
	scaleY := drawHeight / latRange
	scale := math.Min(scaleX, scaleY)
	actualWidth := lonRange * scale
	actualHeight := latRange * scale

	return posterProjection{
		minLat:  minLat,
		minLon:  minLon,
		scale:   scale,
		offsetX: (width-actualWidth)/2 - minLon*scale,
		offsetY: (height-actualHeight)/2 + (minLat+latRange)*scale,
	}, true
}

func (p posterProjection) project(lat, lon float64) (float64, float64) {
	x := lon*p.scale + p.offsetX
	y := p.offsetY - lat*p.scale
	return x, y
}

func buildPosterRoutePath(points []routePreviewPoint, proj posterProjection) (string, float64, float64, float64, float64, bool) {
	projected := projectPosterRoutePoints(points, proj)
	path := posterPathString(projected)
	if path == "" {
		return "", 0, 0, 0, 0, false
	}
	start := projected[0]
	end := projected[len(projected)-1]
	return path, start.X, start.Y, end.X, end.Y, true
}

func buildPosterFactViews(facts []ActivityMapFactView, proj posterProjection) []posterFactView {
	views := make([]posterFactView, 0, len(facts))
	for idx, fact := range facts {
		projectedPath := projectPosterRoutePoints(fact.Path, proj)
		projectedPoints := projectPosterFactPoints(samplePosterFactPoints(fact.Points, posterFactMarkerLimit), proj)
		markerX, markerY, hasMarker := posterFactMarker(projectedPath, projectedPoints)
		views = append(views, posterFactView{
			Index:       idx + 1,
			Title:       fact.Title,
			Summary:     fact.Summary,
			Color:       fact.Color,
			OverlayPath: posterPathString(projectedPath),
			Points:      projectedPoints,
			MarkerX:     markerX,
			MarkerY:     markerY,
			HasMarker:   hasMarker,
		})
	}
	return views
}

func projectPosterRoutePoints(points []routePreviewPoint, proj posterProjection) []posterPoint {
	projected := make([]posterPoint, 0, len(points))
	for _, point := range points {
		x, y := proj.project(point.Lat, point.Lon)
		projected = append(projected, posterPoint{X: x, Y: y})
	}
	return projected
}

func projectPosterFactPoints(points []ActivityFactPoint, proj posterProjection) []posterPoint {
	projected := make([]posterPoint, 0, len(points))
	for _, point := range points {
		x, y := proj.project(point.Lat, point.Lon)
		projected = append(projected, posterPoint{X: x, Y: y})
	}
	return projected
}

func posterPathString(points []posterPoint) string {
	if len(points) < 2 {
		return ""
	}

	var builder strings.Builder
	for idx, point := range points {
		cmd := "L"
		if idx == 0 {
			cmd = "M"
		}
		builder.WriteString(fmt.Sprintf("%s %.1f %.1f ", cmd, point.X, point.Y))
	}
	return strings.TrimSpace(builder.String())
}

func posterFactMarker(pathPoints []posterPoint, factPoints []posterPoint) (float64, float64, bool) {
	switch {
	case len(factPoints) > 0:
		var xTotal float64
		var yTotal float64
		for _, point := range factPoints {
			xTotal += point.X
			yTotal += point.Y
		}
		return xTotal / float64(len(factPoints)), yTotal / float64(len(factPoints)), true
	case len(pathPoints) > 0:
		mid := pathPoints[len(pathPoints)/2]
		return mid.X, mid.Y, true
	default:
		return 0, 0, false
	}
}

func samplePosterFactPoints(points []ActivityFactPoint, max int) []ActivityFactPoint {
	if len(points) <= max || max <= 0 {
		return points
	}

	step := float64(len(points)-1) / float64(max-1)
	sampled := make([]ActivityFactPoint, 0, max)
	lastIdx := -1
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx <= lastIdx {
			idx = lastIdx + 1
		}
		if idx >= len(points) {
			idx = len(points) - 1
		}
		sampled = append(sampled, points[idx])
		lastIdx = idx
		if idx == len(points)-1 {
			break
		}
	}
	return sampled
}
