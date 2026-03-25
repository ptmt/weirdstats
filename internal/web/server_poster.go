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
	"sort"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/storage"
)

const (
	posterMapWidth             = 1000.0
	posterMapHeight            = 1120.0
	posterMapPadding           = 58.0
	posterFactMarkerLimit      = 6
	posterExportWidth          = 1170
	posterExportHeight         = 2532
	posterContextBBoxPaddingM  = 1400.0
	posterContextRoadLimit     = 6
	posterContextWaterLimit    = 4
	posterContextWaterwayLimit = 6
	posterContextPeakLimit     = 4
	posterContextLoadTimeout   = 3 * time.Second
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

type posterLineView struct {
	Name        string
	Path        string
	LabelX      float64
	LabelY      float64
	HasLabel    bool
	StrokeWidth float64
}

type posterAreaView struct {
	Name     string
	Path     string
	LabelX   float64
	LabelY   float64
	HasLabel bool
}

type posterPeakView struct {
	Name string
	X    float64
	Y    float64
}

type posterMapContextView struct {
	Roads     []posterLineView
	Waterways []posterLineView
	Waters    []posterAreaView
	Peaks     []posterPeakView
}

type posterStatView struct {
	Class  string
	Label  string
	Value  string
	Unit   string
	Detail string
}

type posterRenderOptions struct {
	ShowHeader  bool
	ShowMeta    bool
	ShowContext bool
	FactsLimit  int
	Transparent bool
	Uppercase   bool
	Monochrome  bool
}

type posterPageData struct {
	Title            string
	ActivityID       int64
	ActivityName     string
	ActivityType     string
	ActivityTime     string
	Distance         string
	Duration         string
	RoutePath        string
	RouteStartX      float64
	RouteStartY      float64
	RouteEndX        float64
	RouteEndY        float64
	HasRoute         bool
	ShowHeaderBlock  bool
	ShowFactsSection bool
	Roads            []posterLineView
	Waterways        []posterLineView
	Waters           []posterAreaView
	Peaks            []posterPeakView
	Stats            []posterStatView
	Facts            []posterFactView
	Options          posterRenderOptions
	PNGExport        bool
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

	trace := newRequestTrace("poster_html")
	trace.AddField("user_id", userID)
	trace.AddField("activity_id", activityID)
	defer trace.Log()

	options := posterRenderOptionsFromRequest(r)
	trace.AddField("facts_limit", options.FactsLimit)
	trace.AddField("show_context", options.ShowContext)
	trace.AddField("transparent", options.Transparent)
	trace.AddField("uppercase", options.Uppercase)
	trace.AddField("monochrome", options.Monochrome)

	stepStart := time.Now()
	data, err := s.posterPageData(r.Context(), userID, activityID, false, options)
	trace.AddStep("build_page_data", stepStart)
	if errors.Is(err, errPosterActivityNotFound) {
		trace.AddField("error", "activity_not_found")
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err != nil {
		trace.AddField("error", "build_page_data")
		http.Error(w, "failed to build poster", http.StatusInternalServerError)
		return
	}
	trace.AddField("facts", len(data.Facts))
	trace.AddField("has_route", data.HasRoute)

	stepStart = time.Now()
	html, err := s.renderPosterHTML(data)
	trace.AddStep("render_html", stepStart)
	if err != nil {
		trace.AddField("error", "render_html")
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}
	trace.AddField("html_bytes", len(html))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	stepStart = time.Now()
	if _, err := w.Write(html); err != nil {
		trace.AddStep("write_response", stepStart)
		trace.AddField("error", "write_response")
		http.Error(w, "poster write failed", http.StatusInternalServerError)
		return
	}
	trace.AddStep("write_response", stepStart)
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

	trace := newRequestTrace("poster_png")
	trace.AddField("user_id", userID)
	trace.AddField("activity_id", activityID)
	defer trace.Log()

	options := posterRenderOptionsFromRequest(r)
	trace.AddField("facts_limit", options.FactsLimit)
	trace.AddField("show_context", options.ShowContext)
	trace.AddField("transparent", options.Transparent)
	trace.AddField("uppercase", options.Uppercase)
	trace.AddField("monochrome", options.Monochrome)

	stepStart := time.Now()
	data, err := s.posterPageData(r.Context(), userID, activityID, true, options)
	trace.AddStep("build_page_data", stepStart)
	if errors.Is(err, errPosterActivityNotFound) {
		trace.AddField("error", "activity_not_found")
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err != nil {
		trace.AddField("error", "build_page_data")
		http.Error(w, "failed to build poster", http.StatusInternalServerError)
		return
	}
	trace.AddField("facts", len(data.Facts))
	trace.AddField("has_route", data.HasRoute)

	stepStart = time.Now()
	html, err := s.renderPosterHTML(data)
	trace.AddStep("render_html", stepStart)
	if err != nil {
		trace.AddField("error", "render_html")
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}
	trace.AddField("html_bytes", len(html))

	stepStart = time.Now()
	png, err := posterPNGCapture(r.Context(), html)
	trace.AddStep("capture_png", stepStart)
	if errors.Is(err, errPosterBrowserUnavailable) {
		trace.AddField("error", "browser_unavailable")
		http.Error(w, "png export requires Chrome or Chromium on the machine running WeirdStats", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		trace.AddField("error", "capture_png")
		http.Error(w, "png export failed", http.StatusInternalServerError)
		return
	}
	trace.AddField("png_bytes", len(png))

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("weirdstats-activity-%d-poster.png", activityID)))
	stepStart = time.Now()
	if _, err := w.Write(png); err != nil {
		trace.AddStep("write_response", stepStart)
		trace.AddField("error", "write_response")
		http.Error(w, "png write failed", http.StatusInternalServerError)
		return
	}
	trace.AddStep("write_response", stepStart)
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

func posterDefaultRenderOptions() posterRenderOptions {
	return posterRenderOptions{
		ShowHeader:  true,
		ShowMeta:    true,
		ShowContext: true,
		FactsLimit:  -1,
	}
}

func posterRenderOptionsFromRequest(r *http.Request) posterRenderOptions {
	options := posterDefaultRenderOptions()
	values := r.URL.Query()
	options.ShowHeader = posterQueryBool(values, "header", options.ShowHeader)
	options.ShowMeta = posterQueryBool(values, "meta", options.ShowMeta)
	options.ShowContext = posterQueryBool(values, "context", options.ShowContext)
	options.Transparent = posterQueryBool(values, "transparent", false)
	options.Uppercase = posterQueryBool(values, "uppercase", false)
	options.Monochrome = posterQueryBool(values, "mono", false)
	options.FactsLimit = posterFactsLimit(values)
	return options
}

func posterQueryBool(values url.Values, key string, fallback bool) bool {
	rawValues, ok := values[key]
	if !ok || len(rawValues) == 0 {
		return fallback
	}

	switch strings.ToLower(strings.TrimSpace(rawValues[len(rawValues)-1])) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func posterFactsLimit(values url.Values) int {
	rawValues, ok := values["facts"]
	if !ok || len(rawValues) == 0 {
		return -1
	}

	switch strings.ToLower(strings.TrimSpace(rawValues[len(rawValues)-1])) {
	case "", "all":
		return -1
	case "0", "1", "2":
		limit, err := strconv.Atoi(rawValues[len(rawValues)-1])
		if err == nil {
			return limit
		}
	}
	return -1
}

func posterLimitFacts(facts []ActivityMapFactView, limit int) []ActivityMapFactView {
	if limit < 0 || len(facts) <= limit {
		return facts
	}
	if limit == 0 {
		return nil
	}
	return facts[:limit]
}

func buildPosterStats(activity storage.Activity, storedStops []storage.ActivityStop) []posterStatView {
	distanceValue, distanceUnit := formatDistanceParts(activity.Distance)
	speedLabel, speedValue, speedUnit := formatPaceOrSpeed(activity.Type, activity.Distance, activity.MovingTime)
	stats := []posterStatView{
		{
			Class: "story-stat--nw story-stat--distance",
			Label: "Distance",
			Value: distanceValue,
			Unit:  distanceUnit,
		},
		{
			Class: "story-stat--ne story-stat--speed",
			Label: speedLabel,
			Value: speedValue,
			Unit:  speedUnit,
		},
		{
			Class: "story-stat--sw story-stat--moving",
			Label: "Moving",
			Value: formatDuration(activity.MovingTime),
		},
	}

	stopCount := len(storedStops)
	stopTotalSeconds := 0
	trafficLights := 0
	roadCrossings := 0
	for _, stop := range storedStops {
		stopTotalSeconds += stop.DurationSeconds
		if stop.HasTrafficLight {
			trafficLights++
		}
		if stop.HasRoadCrossing {
			roadCrossings++
		}
	}

	stopDetailParts := make([]string, 0, 3)
	if stopTotalSeconds > 0 {
		stopDetailParts = append(stopDetailParts, formatDuration(stopTotalSeconds))
	}
	if trafficLights > 0 {
		stopDetailParts = append(stopDetailParts, formatCountLabel(trafficLights, "light", "lights"))
	}
	if roadCrossings > 0 {
		stopDetailParts = append(stopDetailParts, formatCountLabel(roadCrossings, "crossing", "crossings"))
	}

	if stopCount > 0 || len(stopDetailParts) > 0 {
		stats = append(stats, posterStatView{
			Class:  "story-stat--se story-stat--stops",
			Label:  "Stops",
			Value:  strconv.Itoa(stopCount),
			Unit:   pluralizePosterStat(stopCount, "stop", "stops"),
			Detail: strings.Join(stopDetailParts, " · "),
		})
		return stats
	}

	if value, unit, ok := formatPower(activity.AveragePower); ok {
		stats = append(stats, posterStatView{
			Class: "story-stat--se story-stat--power",
			Label: "Avg power",
			Value: value,
			Unit:  unit,
		})
		return stats
	}

	stats = append(stats, posterStatView{
		Class:  "story-stat--se story-stat--started",
		Label:  "Started",
		Value:  activity.StartTime.Format("15:04"),
		Detail: activity.StartTime.Format("Jan 2"),
	})
	return stats
}

func pluralizePosterStat(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func (s *Server) posterPageData(ctx context.Context, userID, activityID int64, pngExport bool, options posterRenderOptions) (posterPageData, error) {
	trace := newRequestTrace("poster_page_data")
	trace.AddField("user_id", userID)
	trace.AddField("activity_id", activityID)
	trace.AddField("png_export", pngExport)
	trace.AddField("facts_limit", options.FactsLimit)
	trace.AddField("show_context", options.ShowContext)
	defer trace.Log()

	stepStart := time.Now()
	activity, err := s.store.GetActivityForUser(ctx, userID, activityID)
	trace.AddStep("get_activity", stepStart)
	if err != nil {
		trace.AddField("error", "get_activity")
		return posterPageData{}, errPosterActivityNotFound
	}

	stepStart = time.Now()
	points, err := s.store.LoadActivityPoints(ctx, activityID)
	trace.AddStep("load_points", stepStart)
	if err != nil {
		trace.AddField("error", "load_points")
		return posterPageData{}, fmt.Errorf("load points: %w", err)
	}
	trace.AddField("points", len(points))

	stepStart = time.Now()
	storedStops, err := s.store.LoadActivityStops(ctx, activityID)
	trace.AddStep("load_stops", stepStart)
	if err != nil {
		trace.AddField("error", "load_stops")
		return posterPageData{}, fmt.Errorf("load stops: %w", err)
	}
	trace.AddField("stops", len(storedStops))

	stepStart = time.Now()
	detectedFacts, err := s.posterFacts(ctx, activityID, points, storedStops)
	trace.AddStep("load_detected_facts", stepStart)
	if err != nil {
		trace.AddField("error", "load_detected_facts")
		return posterPageData{}, fmt.Errorf("load detected facts: %w", err)
	}
	trace.AddField("facts", len(detectedFacts))

	visibleFacts := posterLimitFacts(detectedFacts, options.FactsLimit)
	trace.AddField("visible_facts", len(visibleFacts))
	posterStats := buildPosterStats(activity, storedStops)
	trace.AddField("stats", len(posterStats))

	stepStart = time.Now()
	routePoints := posterRoutePoints(points)
	routePath := ""
	startX := 0.0
	startY := 0.0
	endX := 0.0
	endY := 0.0
	hasRoute := false
	mapContext := posterMapContextView{}
	projectedFacts := []posterFactView{}

	if proj, ok := newPosterProjection(routePoints, posterMapWidth, posterMapHeight, posterMapPadding); ok {
		if options.ShowContext {
			contextStepStart := time.Now()
			mapContext, err = s.posterMapContext(ctx, activityID, points, proj)
			trace.AddStep("load_map_context", contextStepStart)
			if err != nil {
				trace.AddField("map_context_error", true)
			}
		}
		routePath, startX, startY, endX, endY, hasRoute = buildPosterRoutePath(routePoints, proj)
		projectedFacts = buildPosterFactViews(visibleFacts, proj)
	}
	trace.AddStep("project_poster", stepStart)
	trace.AddField("has_route", hasRoute)
	trace.AddField("context_roads", len(mapContext.Roads))
	trace.AddField("context_waterways", len(mapContext.Waterways))
	trace.AddField("context_waters", len(mapContext.Waters))
	trace.AddField("context_peaks", len(mapContext.Peaks))

	return posterPageData{
		Title:            activity.Name,
		ActivityID:       activity.ID,
		ActivityName:     activity.Name,
		ActivityType:     activity.Type,
		ActivityTime:     activity.StartTime.Format("Jan 2, 2006 15:04"),
		Distance:         formatDistance(activity.Distance),
		Duration:         formatDuration(activity.MovingTime),
		RoutePath:        routePath,
		RouteStartX:      startX,
		RouteStartY:      startY,
		RouteEndX:        endX,
		RouteEndY:        endY,
		HasRoute:         hasRoute,
		ShowHeaderBlock:  options.ShowHeader || options.ShowMeta,
		ShowFactsSection: options.FactsLimit != 0,
		Roads:            mapContext.Roads,
		Waterways:        mapContext.Waterways,
		Waters:           mapContext.Waters,
		Peaks:            mapContext.Peaks,
		Stats:            posterStats,
		Facts:            projectedFacts,
		Options:          options,
		PNGExport:        pngExport,
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
		nil,
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

func (s *Server) posterMapContext(ctx context.Context, activityID int64, points []gps.Point, proj posterProjection) (posterMapContextView, error) {
	trace := newRequestTrace("poster_map_context")
	trace.AddField("activity_id", activityID)
	defer trace.Log()

	if s.overpass == nil || len(points) < 2 {
		trace.AddField("skipped", true)
		return posterMapContextView{}, nil
	}

	bbox, ok := routeBBox(points, posterContextBBoxPaddingM)
	if !ok {
		trace.AddField("skipped", true)
		return posterMapContextView{}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, posterContextLoadTimeout)
	defer cancel()

	stepStart := time.Now()
	contextData, err := s.overpass.FetchMapContext(timeoutCtx, bbox)
	trace.AddStep("fetch_context", stepStart)
	if err != nil {
		trace.AddField("error", "fetch_context")
		return posterMapContextView{}, err
	}

	stepStart = time.Now()
	view := buildPosterMapContextView(contextData, points, proj)
	trace.AddStep("project_context", stepStart)
	trace.AddField("roads", len(view.Roads))
	trace.AddField("waterways", len(view.Waterways))
	trace.AddField("waters", len(view.Waters))
	trace.AddField("peaks", len(view.Peaks))
	trace.AddField("road_names", fmt.Sprintf("%q", posterSelectedLineNames(view.Roads)))
	trace.AddField("waterway_names", fmt.Sprintf("%q", posterSelectedLineNames(view.Waterways)))
	trace.AddField("water_names", fmt.Sprintf("%q", posterSelectedAreaNames(view.Waters)))
	trace.AddField("peak_names", fmt.Sprintf("%q", posterSelectedPeakNames(view.Peaks)))
	return view, nil
}

func buildPosterMapContextView(contextData maps.MapContext, routePoints []gps.Point, proj posterProjection) posterMapContextView {
	view := posterMapContextView{
		Roads:     make([]posterLineView, 0, posterContextRoadLimit),
		Waterways: make([]posterLineView, 0, posterContextWaterwayLimit),
		Waters:    make([]posterAreaView, 0, posterContextWaterLimit),
		Peaks:     make([]posterPeakView, 0, posterContextPeakLimit),
	}

	for _, road := range selectPosterRoads(contextData.Roads, routePoints, posterContextRoadLimit) {
		if lineView, ok := buildPosterLineView(road.Name, road.Geometry, proj, posterRoadStrokeWidth(road.Highway)); ok {
			view.Roads = append(view.Roads, lineView)
		}
	}
	for _, waterway := range selectPosterWaterways(contextData.Waterways, routePoints, posterContextWaterwayLimit) {
		if lineView, ok := buildPosterLineView(waterway.Name, waterway.Geometry, proj, 5.5); ok {
			view.Waterways = append(view.Waterways, lineView)
		}
	}
	for _, water := range selectPosterWaters(contextData.Waters, routePoints, posterContextWaterLimit) {
		if areaView, ok := buildPosterAreaView(water.Name, water.Geometry, proj); ok {
			view.Waters = append(view.Waters, areaView)
		}
	}
	for _, peak := range selectPosterPeaks(contextData.Peaks, routePoints, posterContextPeakLimit) {
		x, y := proj.project(peak.Lat, peak.Lon)
		view.Peaks = append(view.Peaks, posterPeakView{
			Name: peak.Name,
			X:    x,
			Y:    y,
		})
	}
	return view
}

func selectPosterRoads(roads []maps.Road, routePoints []gps.Point, max int) []maps.Road {
	if len(roads) == 0 || max <= 0 {
		return nil
	}

	selected := append([]maps.Road(nil), roads...)
	sortPosterRoads(selected, routePoints)
	if len(selected) > max {
		selected = selected[:max]
	}
	return selected
}

func selectPosterWaterways(features []maps.PolylineFeature, routePoints []gps.Point, max int) []maps.PolylineFeature {
	if len(features) == 0 || max <= 0 {
		return nil
	}

	selected := append([]maps.PolylineFeature(nil), features...)
	sortPosterPolylineFeatures(selected, routePoints)
	if len(selected) > max {
		selected = selected[:max]
	}
	return selected
}

func selectPosterWaters(features []maps.PolygonFeature, routePoints []gps.Point, max int) []maps.PolygonFeature {
	if len(features) == 0 || max <= 0 {
		return nil
	}

	selected := append([]maps.PolygonFeature(nil), features...)
	sortPosterPolygonFeatures(selected, routePoints)
	if len(selected) > max {
		selected = selected[:max]
	}
	return selected
}

func selectPosterPeaks(peaks []maps.POI, routePoints []gps.Point, max int) []maps.POI {
	if max <= 0 {
		return nil
	}
	selected := append([]maps.POI(nil), peaks...)
	sortPosterPeaks(selected, routePoints)
	if len(selected) > max {
		selected = selected[:max]
	}
	return selected
}

func sortPosterRoads(roads []maps.Road, routePoints []gps.Point) {
	sort.Slice(roads, func(i, j int) bool {
		leftDistance := posterRouteDistanceForGeometry(roads[i].Geometry, routePoints)
		rightDistance := posterRouteDistanceForGeometry(roads[j].Geometry, routePoints)
		leftDistanceBucket := posterContextDistanceBucket(leftDistance)
		rightDistanceBucket := posterContextDistanceBucket(rightDistance)
		if leftDistanceBucket != rightDistanceBucket {
			return leftDistanceBucket < rightDistanceBucket
		}
		leftRank := posterRoadRank(roads[i].Highway)
		rightRank := posterRoadRank(roads[j].Highway)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		leftNamed := posterNamedRank(roads[i].Name)
		rightNamed := posterNamedRank(roads[j].Name)
		if leftNamed != rightNamed {
			return leftNamed > rightNamed
		}
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		if len(roads[i].Geometry) != len(roads[j].Geometry) {
			return len(roads[i].Geometry) > len(roads[j].Geometry)
		}
		return roads[i].Name < roads[j].Name
	})
}

func sortPosterPolylineFeatures(features []maps.PolylineFeature, routePoints []gps.Point) {
	sort.Slice(features, func(i, j int) bool {
		leftDistance := posterRouteDistanceForGeometry(features[i].Geometry, routePoints)
		rightDistance := posterRouteDistanceForGeometry(features[j].Geometry, routePoints)
		leftDistanceBucket := posterContextDistanceBucket(leftDistance)
		rightDistanceBucket := posterContextDistanceBucket(rightDistance)
		if leftDistanceBucket != rightDistanceBucket {
			return leftDistanceBucket < rightDistanceBucket
		}
		leftNamed := posterNamedRank(features[i].Name)
		rightNamed := posterNamedRank(features[j].Name)
		if leftNamed != rightNamed {
			return leftNamed > rightNamed
		}
		leftRank := posterWaterwayRank(features[i].Kind)
		rightRank := posterWaterwayRank(features[j].Kind)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		if len(features[i].Geometry) != len(features[j].Geometry) {
			return len(features[i].Geometry) > len(features[j].Geometry)
		}
		return features[i].Name < features[j].Name
	})
}

func sortPosterPolygonFeatures(features []maps.PolygonFeature, routePoints []gps.Point) {
	sort.Slice(features, func(i, j int) bool {
		leftDistance := posterRouteDistanceForGeometry(features[i].Geometry, routePoints)
		rightDistance := posterRouteDistanceForGeometry(features[j].Geometry, routePoints)
		leftDistanceBucket := posterContextDistanceBucket(leftDistance)
		rightDistanceBucket := posterContextDistanceBucket(rightDistance)
		if leftDistanceBucket != rightDistanceBucket {
			return leftDistanceBucket < rightDistanceBucket
		}
		leftNamed := posterNamedRank(features[i].Name)
		rightNamed := posterNamedRank(features[j].Name)
		if leftNamed != rightNamed {
			return leftNamed > rightNamed
		}
		leftRank := posterWaterAreaRank(features[i].Kind)
		rightRank := posterWaterAreaRank(features[j].Kind)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		if len(features[i].Geometry) != len(features[j].Geometry) {
			return len(features[i].Geometry) > len(features[j].Geometry)
		}
		return features[i].Name < features[j].Name
	})
}

func sortPosterPeaks(peaks []maps.POI, routePoints []gps.Point) {
	sort.Slice(peaks, func(i, j int) bool {
		leftDistance := minDistanceToRouteMeters(peaks[i].Lat, peaks[i].Lon, routePoints)
		rightDistance := minDistanceToRouteMeters(peaks[j].Lat, peaks[j].Lon, routePoints)
		leftDistanceBucket := posterContextDistanceBucket(leftDistance)
		rightDistanceBucket := posterContextDistanceBucket(rightDistance)
		if leftDistanceBucket != rightDistanceBucket {
			return leftDistanceBucket < rightDistanceBucket
		}
		leftNamed := posterNamedRank(peaks[i].Name)
		rightNamed := posterNamedRank(peaks[j].Name)
		if leftNamed != rightNamed {
			return leftNamed > rightNamed
		}
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		return peaks[i].Name < peaks[j].Name
	})
}

func posterRoadRank(highway string) int {
	switch highway {
	case "motorway", "trunk", "motorway_link", "trunk_link":
		return 4
	case "primary", "primary_link":
		return 3
	case "secondary", "secondary_link":
		return 2
	case "tertiary", "tertiary_link":
		return 1
	default:
		return 0
	}
}

func posterRoadStrokeWidth(highway string) float64 {
	switch highway {
	case "motorway", "trunk", "motorway_link", "trunk_link":
		return 8.5
	case "primary", "primary_link":
		return 7.5
	case "secondary", "secondary_link":
		return 6.5
	default:
		return 5.5
	}
}

func posterNamedRank(name string) int {
	if strings.TrimSpace(name) == "" {
		return 0
	}
	return 1
}

func posterWaterwayRank(kind string) int {
	switch kind {
	case "river":
		return 3
	case "canal":
		return 2
	case "stream":
		return 1
	default:
		return 0
	}
}

func posterWaterAreaRank(kind string) int {
	switch kind {
	case "riverbank":
		return 3
	case "water":
		return 2
	case "reservoir":
		return 1
	default:
		return 0
	}
}

func posterContextDistanceBucket(distance float64) int {
	switch {
	case distance <= 80:
		return 0
	case distance <= 180:
		return 1
	case distance <= 320:
		return 2
	case distance <= 600:
		return 3
	default:
		return 4
	}
}

func posterRouteDistanceForGeometry(geometry []maps.LatLon, routePoints []gps.Point) float64 {
	if len(routePoints) == 0 || len(geometry) == 0 {
		return math.Inf(1)
	}
	best := math.Inf(1)
	for _, point := range samplePosterLatLonPoints(geometry, 12) {
		distance := minDistanceToRouteMeters(point.Lat, point.Lon, routePoints)
		if distance < best {
			best = distance
		}
	}
	return best
}

func samplePosterLatLonPoints(points []maps.LatLon, max int) []maps.LatLon {
	if len(points) <= max || max <= 0 {
		return points
	}

	step := float64(len(points)-1) / float64(max-1)
	sampled := make([]maps.LatLon, 0, max)
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

func posterSelectedLineNames(lines []posterLineView) string {
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line.Name) == "" {
			continue
		}
		names = append(names, line.Name)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, " | ")
}

func posterSelectedAreaNames(areas []posterAreaView) string {
	names := make([]string, 0, len(areas))
	for _, area := range areas {
		if strings.TrimSpace(area.Name) == "" {
			continue
		}
		names = append(names, area.Name)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, " | ")
}

func posterSelectedPeakNames(peaks []posterPeakView) string {
	names := make([]string, 0, len(peaks))
	for _, peak := range peaks {
		if strings.TrimSpace(peak.Name) == "" {
			continue
		}
		names = append(names, peak.Name)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, " | ")
}

func buildPosterLineView(name string, geometry []maps.LatLon, proj posterProjection, strokeWidth float64) (posterLineView, bool) {
	projected := projectPosterLatLonPoints(geometry, proj)
	path := posterPathString(projected)
	if path == "" {
		return posterLineView{}, false
	}
	labelX, labelY, hasLabel := posterLineLabel(projected, name)
	return posterLineView{
		Name:        name,
		Path:        path,
		LabelX:      labelX,
		LabelY:      labelY,
		HasLabel:    hasLabel,
		StrokeWidth: strokeWidth,
	}, true
}

func buildPosterAreaView(name string, geometry []maps.LatLon, proj posterProjection) (posterAreaView, bool) {
	projected := projectPosterLatLonPoints(geometry, proj)
	path := posterClosedPathString(projected)
	if path == "" {
		return posterAreaView{}, false
	}
	labelX, labelY, hasLabel := posterAreaLabel(projected, name)
	return posterAreaView{
		Name:     name,
		Path:     path,
		LabelX:   labelX,
		LabelY:   labelY,
		HasLabel: hasLabel,
	}, true
}

func projectPosterLatLonPoints(points []maps.LatLon, proj posterProjection) []posterPoint {
	projected := make([]posterPoint, 0, len(points))
	for _, point := range points {
		x, y := proj.project(point.Lat, point.Lon)
		projected = append(projected, posterPoint{X: x, Y: y})
	}
	return projected
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

func posterClosedPathString(points []posterPoint) string {
	if len(points) < 3 {
		return ""
	}
	return posterPathString(points) + " Z"
}

func posterLineLabel(points []posterPoint, name string) (float64, float64, bool) {
	if len(points) == 0 || strings.TrimSpace(name) == "" {
		return 0, 0, false
	}
	mid := points[len(points)/2]
	return mid.X, mid.Y, true
}

func posterAreaLabel(points []posterPoint, name string) (float64, float64, bool) {
	if len(points) == 0 || strings.TrimSpace(name) == "" {
		return 0, 0, false
	}
	var xTotal float64
	var yTotal float64
	for _, point := range points {
		xTotal += point.X
		yTotal += point.Y
	}
	return xTotal / float64(len(points)), yTotal / float64(len(points)), true
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
	trace := newRequestTrace("poster_png_capture")
	trace.AddField("html_bytes", len(html))
	defer trace.Log()

	stepStart := time.Now()
	browserPath, browserProbe, err := findPosterBrowser()
	trace.AddStep("find_browser", stepStart)
	trace.AddField("browser_probe", browserProbe)
	if err != nil {
		trace.AddField("error", "find_browser")
		return nil, err
	}
	trace.AddField("browser", fmt.Sprintf("%q", filepath.Base(browserPath)))
	trace.AddField("browser_path", fmt.Sprintf("%q", browserPath))

	stepStart = time.Now()
	tempDir, err := os.MkdirTemp("", "weirdstats-poster-*")
	trace.AddStep("create_temp_dir", stepStart)
	if err != nil {
		trace.AddField("error", "create_temp_dir")
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	htmlPath := filepath.Join(tempDir, "poster.html")
	stepStart = time.Now()
	if err := os.WriteFile(htmlPath, html, 0o600); err != nil {
		trace.AddStep("write_html", stepStart)
		trace.AddField("error", "write_html")
		return nil, err
	}
	trace.AddStep("write_html", stepStart)

	targetURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(htmlPath)}).String()
	cmd := exec.CommandContext(
		ctx,
		browserPath,
		"--headless",
		"--disable-gpu",
		"--hide-scrollbars",
		"--default-background-color=00000000",
		fmt.Sprintf("--window-size=%d,%d", posterExportWidth, posterExportHeight),
		"--screenshot",
		targetURL,
	)
	cmd.Dir = tempDir
	stepStart = time.Now()
	output, err := cmd.CombinedOutput()
	trace.AddStep("headless_screenshot", stepStart)
	if err != nil {
		trace.AddField("error", "headless_screenshot")
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}

	pngPath := filepath.Join(tempDir, "screenshot.png")
	stepStart = time.Now()
	png, err := os.ReadFile(pngPath)
	trace.AddStep("read_png", stepStart)
	if err != nil {
		trace.AddField("error", "read_png")
		return nil, err
	}
	trace.AddField("png_bytes", len(png))
	return png, nil
}

func findPosterBrowser() (string, string, error) {
	probes := make([]string, 0, len(posterBrowserCandidates))
	for _, candidate := range posterBrowserCandidates {
		label := posterBrowserProbeLabel(candidate)
		if strings.Contains(candidate, string(filepath.Separator)) {
			if _, err := os.Stat(candidate); err == nil {
				probes = append(probes, label+":hit")
				return candidate, strings.Join(probes, "|"), nil
			}
			probes = append(probes, label+":miss")
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			probes = append(probes, label+":hit")
			return path, strings.Join(probes, "|"), nil
		}
		probes = append(probes, label+":miss")
	}
	return "", strings.Join(probes, "|"), errPosterBrowserUnavailable
}

func posterBrowserProbeLabel(value string) string {
	return strings.ReplaceAll(value, " ", "_")
}
