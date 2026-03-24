package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	posterExportWidth     = 1170
	posterExportHeight    = 2532
)

var (
	errPosterActivityNotFound   = errors.New("poster activity not found")
	errPosterBrowserUnavailable = errors.New("poster browser unavailable")
	posterPNGCapture            = capturePosterPNGWithHeadlessBrowser
	posterBrowserCandidates     = []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"chrome",
		"msedge",
		"microsoft-edge",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}
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
	PNGExport    bool
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

	activityID, err := posterActivityID(r.URL.Path, "/poster")
	if err != nil {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	data, err := s.posterPageData(r.Context(), userID, activityID, false)
	if errors.Is(err, errPosterActivityNotFound) {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to build poster", http.StatusInternalServerError)
		return
	}

	html, err := s.renderPosterHTML(data)
	if err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(html); err != nil {
		http.Error(w, "poster write failed", http.StatusInternalServerError)
	}
}

func (s *Server) ActivityPosterPNG(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}

	activityID, err := posterActivityID(r.URL.Path, "/poster.png")
	if err != nil {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	data, err := s.posterPageData(r.Context(), userID, activityID, true)
	if errors.Is(err, errPosterActivityNotFound) {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to build poster", http.StatusInternalServerError)
		return
	}

	html, err := s.renderPosterHTML(data)
	if err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}

	png, err := posterPNGCapture(r.Context(), html)
	if errors.Is(err, errPosterBrowserUnavailable) {
		http.Error(w, "png export requires Chrome or Chromium installed locally", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "png export failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("weirdstats-activity-%d-poster.png", activityID)))
	if _, err := w.Write(png); err != nil {
		http.Error(w, "png write failed", http.StatusInternalServerError)
	}
}

func posterActivityID(path, suffix string) (int64, error) {
	idStr := strings.TrimPrefix(path, "/activity/")
	idStr = strings.TrimSuffix(idStr, suffix)
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		return 0, errors.New("invalid activity id")
	}
	return activityID, nil
}

func (s *Server) posterPageData(ctx context.Context, userID, activityID int64, pngExport bool) (posterPageData, error) {
	activity, err := s.store.GetActivityForUser(ctx, userID, activityID)
	if err != nil {
		return posterPageData{}, errPosterActivityNotFound
	}

	points, err := s.store.LoadActivityPoints(ctx, activityID)
	if err != nil {
		return posterPageData{}, fmt.Errorf("load points: %w", err)
	}
	storedStops, err := s.store.LoadActivityStops(ctx, activityID)
	if err != nil {
		return posterPageData{}, fmt.Errorf("load stops: %w", err)
	}

	detectedFacts, err := s.posterFacts(ctx, activityID, points, storedStops)
	if err != nil {
		return posterPageData{}, fmt.Errorf("load detected facts: %w", err)
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

	return posterPageData{
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
		PNGExport:    pngExport,
	}, nil
}

func (s *Server) renderPosterHTML(data posterPageData) ([]byte, error) {
	var buf bytes.Buffer
	if err := s.templates["poster"].ExecuteTemplate(&buf, "poster", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

func capturePosterPNGWithHeadlessBrowser(ctx context.Context, html []byte) ([]byte, error) {
	browserPath, err := findPosterBrowser()
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "weirdstats-poster-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	htmlPath := filepath.Join(tempDir, "poster.html")
	if err := os.WriteFile(htmlPath, html, 0o600); err != nil {
		return nil, err
	}

	targetURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(htmlPath)}).String()
	cmd := exec.CommandContext(
		ctx,
		browserPath,
		"--headless",
		"--disable-gpu",
		"--hide-scrollbars",
		fmt.Sprintf("--window-size=%d,%d", posterExportWidth, posterExportHeight),
		"--screenshot",
		targetURL,
	)
	cmd.Dir = tempDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}

	pngPath := filepath.Join(tempDir, "screenshot.png")
	png, err := os.ReadFile(pngPath)
	if err != nil {
		return nil, err
	}
	return png, nil
}

func findPosterBrowser() (string, error) {
	for _, candidate := range posterBrowserCandidates {
		if strings.Contains(candidate, string(filepath.Separator)) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", errPosterBrowserUnavailable
}
