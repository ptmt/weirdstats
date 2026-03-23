package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/jobs"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func (s *Server) Activities(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/activities" {
		http.Redirect(w, r, "/activities/", http.StatusFound)
		return
	}
	if r.URL.Path != "/activities/" {
		http.NotFound(w, r)
		return
	}
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	activities, err := s.store.ListActivitiesWithStats(r.Context(), userID, 100)
	if err != nil {
		http.Error(w, "failed to load activities", http.StatusInternalServerError)
		return
	}
	var views []ActivityView
	activityIDs := make([]int64, 0, len(activities))
	for _, activity := range activities {
		activityIDs = append(activityIDs, activity.ID)
	}
	routePointsByActivity, err := s.store.ListActivityRoutePreviewPoints(r.Context(), activityIDs, 48)
	if err != nil {
		log.Printf("route preview load failed: %v", err)
		routePointsByActivity = map[int64][]storage.ActivityRoutePoint{}
	}
	for _, activity := range activities {
		stravaDescription, detectedFactCount := splitStoredActivityDescription(activity.Description)
		feedDescription := stravaDescription
		if feedDescription == "" {
			feedDescription = strings.TrimSpace(activity.Description)
		}
		view := ActivityView{
			ID:                activity.ID,
			Name:              activity.Name,
			Type:              activity.Type,
			StartTime:         activity.StartTime.Format("Jan 2, 2006 15:04"),
			Description:       activity.Description,
			StravaDescription: feedDescription,
			Distance:          formatDistance(activity.Distance),
			Duration:          formatDuration(activity.MovingTime),
			HasStats:          activity.HasStats,
			StopCount:         activity.StopCount,
			StopTotal:         formatDuration(activity.StopTotalSeconds),
			LightStops:        activity.TrafficLightStopCount,
			RoadCrossings:     activity.RoadCrossingCount,
			DetectedFactCount: detectedFactCount,
			PhotoURL:          activity.PhotoURL,
		}
		enrichActivityView(&view, activity.Activity)
		routePoints := routePointsByActivity[activity.ID]
		if len(routePoints) > 0 {
			previewPoints := make([]routePreviewPoint, 0, len(routePoints))
			for _, p := range routePoints {
				previewPoints = append(previewPoints, routePreviewPoint{
					Lat: p.Lat,
					Lon: p.Lon,
				})
			}
			pointsJSON, err := json.Marshal(previewPoints)
			if err != nil {
				log.Printf("route preview marshal failed for activity %d: %v", activity.ID, err)
				view.RoutePreviewJSON = "[]"
			} else {
				view.RoutePreviewJSON = template.JS(pointsJSON)
				view.HasRoutePreview = true
			}
		}
		if path, startX, startY, endX, endY, ok := buildRoutePreviewPath(routePoints, 188, 120, 8); ok {
			view.RoutePath = path
			view.RouteStartX = startX
			view.RouteStartY = startY
			view.RouteEndX = endX
			view.RouteEndY = endY
		}
		views = append(views, view)
	}
	now := time.Now()
	years, err := s.store.ListActivityYears(r.Context(), userID)
	if err != nil {
		log.Printf("contrib years load failed: %v", err)
	}
	currentYear := now.Year()
	seenYears := map[int]bool{currentYear: true}
	orderedYears := []int{currentYear}
	for _, year := range years {
		if !seenYears[year] {
			orderedYears = append(orderedYears, year)
			seenYears[year] = true
		}
	}
	var contribs []ContributionData
	for _, year := range orderedYears {
		contribs = append(contribs, s.buildContributionDataForYear(r.Context(), userID, year, now))
	}
	data := ProfilePageData{
		PageData: PageData{
			Title:      "Activities",
			Page:       "activities",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Tip: the worker runs in the background and fills in stats after ingest.",
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
		Activities:    views,
		Contributions: contribs,
	}
	if err := s.templates["profile"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) ActivityDetail(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/activity/")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	segments := strings.Split(path, "/")
	idStr := segments[0]
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		http.NotFound(w, r)
		return
	}
	if len(segments) > 1 {
		http.NotFound(w, r)
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

	stopViews := buildStopViews(storedStops)

	detectedFacts := []ActivityMapFactView{}
	detectedFactsPresent := false
	rawDetectedFactsJSON, _, err := s.store.GetActivityDetectedFacts(r.Context(), activityID)
	if err == nil {
		detectedFactsPresent = true
		if strings.TrimSpace(rawDetectedFactsJSON) != "" {
			if err := json.Unmarshal([]byte(rawDetectedFactsJSON), &detectedFacts); err != nil {
				log.Printf("detected facts cache decode failed for activity %d: %v", activityID, err)
				detectedFacts = nil
				detectedFactsPresent = false
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "failed to load detected facts", http.StatusInternalServerError)
		return
	}

	type mapPoint struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	var routePoints []mapPoint
	for _, p := range points {
		routePoints = append(routePoints, mapPoint{Lat: p.Lat, Lon: p.Lon})
	}

	pointsJSON, _ := json.Marshal(routePoints)
	stopsJSON, _ := json.Marshal(stopViews)
	detectedFactsJSON, _ := json.Marshal(detectedFacts)
	type speedPoint struct {
		T float64 `json:"t"`
		S float64 `json:"s"`
	}
	var speeds []speedPoint
	if len(points) > 0 {
		startTs := points[0].Time
		for _, p := range points {
			speeds = append(speeds, speedPoint{
				T: p.Time.Sub(startTs).Seconds(),
				S: p.Speed,
			})
		}
	}
	speedJSON, _ := json.Marshal(speeds)

	statsSnapshot := stats.StopStats{}
	statsPresent := false
	recalculatedAt := ""
	statsSnapshot, err = s.store.GetActivityStats(r.Context(), activityID)
	if err == nil {
		statsPresent = true
		recalculatedAt = formatTimestamp(statsSnapshot.UpdatedAt)
	} else if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "failed to load activity stats", http.StatusInternalServerError)
		return
	}

	stopCount := len(stopViews)
	stopTotalSeconds := totalStopSeconds(stopViews)
	lightStops := countLightStops(stopViews)
	roadCrossings := countRoadCrossings(stopViews)
	if statsPresent {
		stopCount = statsSnapshot.StopCount
		stopTotalSeconds = statsSnapshot.StopTotalSeconds
		lightStops = statsSnapshot.TrafficLightStopCount
		roadCrossings = statsSnapshot.RoadCrossingCount
	}

	view := ActivityView{
		ID:                activity.ID,
		Name:              activity.Name,
		Type:              activity.Type,
		StartTime:         activity.StartTime.Format("Jan 2, 2006 15:04"),
		Distance:          formatDistance(activity.Distance),
		Duration:          formatDuration(activity.MovingTime),
		HasStats:          statsPresent,
		StopCount:         stopCount,
		StopTotal:         formatDuration(stopTotalSeconds),
		LightStops:        lightStops,
		DetectedFactCount: len(detectedFacts),
		RoadCrossings:     roadCrossings,
		RecalculatedAt:    recalculatedAt,
		FetchedAt:         formatTimestamp(activity.UpdatedAt),
	}
	enrichActivityView(&view, activity)

	dataItems := buildActivityDataItems(
		activity.Description,
		points,
		storedStops,
		statsSnapshot,
		statsPresent,
		detectedFacts,
		detectedFactsPresent,
		s.stopOpts,
		s.mapAPI != nil,
		s.overpass != nil,
	)

	footerText := "Last recalculation: "
	if view.RecalculatedAt != "" {
		footerText += view.RecalculatedAt
	} else {
		footerText += "pending"
	}
	if view.FetchedAt != "" {
		footerText += " · Last fetch: " + view.FetchedAt
	}

	data := ActivityDetailData{
		PageData: PageData{
			Title:      activity.Name,
			Page:       "activity",
			Message:    r.URL.Query().Get("msg"),
			FooterText: footerText,
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
		Activity:          view,
		Stops:             stopViews,
		DetectedFacts:     detectedFacts,
		DataItems:         dataItems,
		RoutePointsJSON:   template.JS(pointsJSON),
		StopsJSON:         template.JS(stopsJSON),
		DetectedFactsJSON: template.JS(detectedFactsJSON),
		SpeedSeriesJSON:   template.JS(speedJSON),
		SpeedThreshold:    s.stopOpts.SpeedThreshold,
		StopMinDuration:   formatDuration(int(s.stopOpts.MinDuration.Seconds())),
		HasRoutePoints:    len(routePoints) > 0,
		HasSpeedSeries:    len(speeds) > 0,
	}

	if err := s.templates["activity"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

// Activity dispatches to either detail view or download based on path.
func (s *Server) Activity(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/download") {
		s.DownloadActivity(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/refresh") {
		s.RefreshActivity(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/apply") {
		s.ApplyActivityRules(w, r)
		return
	}
	s.ActivityDetail(w, r)
}

func (s *Server) RefreshActivity(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	idStr = strings.TrimSuffix(idStr, "/refresh")
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	if _, err := s.store.GetActivityForUser(r.Context(), userID, activityID); err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if err := jobs.EnqueueProcessActivity(r.Context(), s.store, activityID, userID); err != nil {
		http.Error(w, "failed to enqueue activity", http.StatusInternalServerError)
		return
	}

	redirectURL := r.Header.Get("Referer")
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("/activity/%d", activityID)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) DownloadActivity(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	idStr = strings.TrimSuffix(idStr, "/download")
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

	download := ActivityDownload{
		ID:          activity.ID,
		Type:        activity.Type,
		Name:        activity.Name,
		StartTime:   activity.StartTime,
		Description: activity.Description,
		Distance:    activity.Distance,
		MovingTime:  activity.MovingTime,
		Points:      make([]PointDownload, len(points)),
	}
	for i, p := range points {
		download.Points[i] = PointDownload{
			Lat:   p.Lat,
			Lon:   p.Lon,
			Time:  p.Time,
			Speed: p.Speed,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="activity_%d.json"`, activityID))

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(download); err != nil {
		log.Printf("failed to encode activity download: %v", err)
	}
}
