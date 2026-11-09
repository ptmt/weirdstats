package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"weirdstats/internal/storage"
)

type mobileActivityDetailResponse struct {
	Activity         mobileActivityView `json:"activity"`
	Stops            []StopView         `json:"stops"`
	StatsStatus      string             `json:"stats_status"`
	StopTotal        string             `json:"stop_total,omitempty"`
	PaceLabel        string             `json:"pace_label,omitempty"`
	PaceValue        string             `json:"pace_value,omitempty"`
	PaceUnit         string             `json:"pace_unit,omitempty"`
	AverageHeartRate *float64           `json:"average_heart_rate,omitempty"`
	AveragePower     *float64           `json:"average_power,omitempty"`
	Pacing           *RidePacingView    `json:"pacing,omitempty"`
}

// MobileActivityDetail loads only persisted, owner-scoped evidence. It never
// fetches Strava or recalculates missing facts as a side effect of viewing.
func (s *Server) MobileActivityDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := s.requireAPIUserID(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/mobile/activities/")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || raw != strconv.FormatInt(id, 10) {
		http.NotFound(w, r)
		return
	}
	activity, err := s.store.GetActivityForUser(r.Context(), userID, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to load activity", 500)
		return
	}
	points, err := s.store.LoadActivityPoints(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to load route", 500)
		return
	}
	stops, err := s.store.LoadActivityStops(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to load stops", 500)
		return
	}
	item := buildMobileActivityView(storage.ActivityWithStats{Activity: activity})
	item.Route = []routePreviewPoint{}
	item.Facts = []ActivityMapFactView{}
	item.RouteStatus = "no_gps"
	item.FactsStatus = "pending"
	for _, p := range points {
		if !math.IsNaN(p.Lat) && !math.IsNaN(p.Lon) && math.Abs(p.Lat) <= 90 && math.Abs(p.Lon) <= 180 {
			item.Route = append(item.Route, routePreviewPoint{Lat: p.Lat, Lon: p.Lon})
		}
	}
	if len(item.Route) > 1 {
		item.RouteStatus = "available"
	}
	facts, _, err := s.store.GetActivityDetectedFacts(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "failed to load facts", 500)
		return
	}
	if err == nil && strings.TrimSpace(facts) != "" {
		if json.Unmarshal([]byte(facts), &item.Facts) != nil {
			http.Error(w, "failed to decode facts", 500)
			return
		}
		if item.Facts == nil {
			item.Facts = []ActivityMapFactView{}
		}
		refreshRideSegmentFactSummary(item.Facts, activity.Type, points, s.stopOpts)
		item.FactsStatus = "available"
	}
	item.DetectedFactCount = len(item.Facts)
	stopViews := buildStopViews(stops)
	if stopViews == nil {
		stopViews = []StopView{}
	}
	result := mobileActivityDetailResponse{Activity: item, Stops: stopViews, StatsStatus: "pending", Pacing: buildRidePacingView(activity, points)}
	snapshot, err := s.store.GetActivityStats(r.Context(), id)
	if err == nil {
		result.StatsStatus = "available"
		result.StopTotal = formatDuration(snapshot.StopTotalSeconds)
		result.Activity.StopCount = snapshot.StopCount
		result.Activity.LightStops = snapshot.TrafficLightStopCount
		result.Activity.RoadCrossings = snapshot.RoadCrossingCount
	} else if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "failed to load stats", 500)
		return
	}
	if activity.Distance > 0 && activity.MovingTime > 0 {
		result.PaceLabel, result.PaceValue, result.PaceUnit = formatPaceOrSpeed(activity.Type, activity.Distance, activity.MovingTime)
	}
	if activity.AverageHeartRate > 0 {
		result.AverageHeartRate = &activity.AverageHeartRate
	}
	if activity.AveragePower > 0 {
		result.AveragePower = &activity.AveragePower
	}
	writeJSON(w, 200, result)
}
