package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/jobs"
	"weirdstats/internal/maps"
	"weirdstats/internal/rules"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/**
var staticFS embed.FS

//go:embed static/*
var StaticFS embed.FS

const (
	sessionCookieName    = "weirdstats_session"
	oauthStateCookieName = "weirdstats_oauth_state"
	oauthNextCookieName  = "weirdstats_oauth_next"
	sessionDuration      = 30 * 24 * time.Hour
)

type Server struct {
	store         *storage.Store
	ingestor      *ingest.Ingestor
	mapAPI        maps.API
	overpass      *maps.OverpassClient
	stopOpts      gps.StopOptions
	templates     map[string]*template.Template
	strava        StravaConfig
	sessionSecret []byte
}

type ActivityView struct {
	ID                int64
	Name              string
	Type              string
	TypeLabel         string
	TypeClass         string
	StartTime         string
	Description       string
	StravaDescription string
	Distance          string
	DistanceValue     string
	DistanceUnit      string
	Duration          string
	PaceLabel         string
	PaceValue         string
	PaceUnit          string
	PowerValue        string
	PowerUnit         string
	HasPower          bool
	HasStats          bool
	StopCount         int
	StopTotal         string
	LightStops        int
	DetectedFactCount int
	RoadCrossings     int
	RecalculatedAt    string
	FetchedAt         string
	IsHidden          bool
	FeedMuted         bool
	PhotoURL          string
	HasRoutePreview   bool
	RoutePath         string
	RouteStartX       float64
	RouteStartY       float64
	RouteEndX         float64
	RouteEndY         float64
	RoutePreviewJSON  template.JS
}

type StopView struct {
	Lat             float64 `json:"lat"`
	Lon             float64 `json:"lon"`
	StartSeconds    float64 `json:"start_seconds"`
	Duration        string  `json:"duration"`
	DurationSeconds int     `json:"duration_seconds"`
	HasTrafficLight bool    `json:"has_traffic_light"`
	HasRoadCrossing bool    `json:"has_road_crossing"`
	CrossingRoad    string  `json:"crossing_road,omitempty"`
}

type ActivityFactPoint struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Label string  `json:"label,omitempty"`
}

type ActivityMapFactView struct {
	ID      string              `json:"id"`
	Kind    string              `json:"kind"`
	Title   string              `json:"title"`
	Summary string              `json:"summary"`
	Color   string              `json:"color"`
	Points  []ActivityFactPoint `json:"points,omitempty"`
	Path    []routePreviewPoint `json:"path,omitempty"`
}

type routePreviewPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type ActivityDetailData struct {
	PageData
	Activity          ActivityView
	Stops             []StopView
	DetectedFacts     []ActivityMapFactView
	RoutePointsJSON   template.JS
	StopsJSON         template.JS
	DetectedFactsJSON template.JS
	SpeedSeriesJSON   template.JS
	SpeedThreshold    float64
}

type StravaInfo struct {
	Connected   bool
	AthleteID   int64
	AthleteName string
}

type PageData struct {
	Title      string
	Page       string
	Message    string
	FooterText string
	Strava     StravaInfo
	UserCount  int
}

type LandingPageData struct {
	PageData
}

type ProfilePageData struct {
	PageData
	Activities    []ActivityView
	Contributions []ContributionData
}

type SettingsRule struct {
	ID          int64
	Name        string
	Description string
	Enabled     bool
	IsLegacy    bool
}

type SettingsPageData struct {
	PageData
	Facts         []SettingsFact
	Rules         []SettingsRule
	RulesMetaJSON template.JS
}

type AdminPageData struct {
	PageData
	QueueCount   int
	Jobs         []JobView
	ActivityJobs []JobView
}

type ContributionDay struct {
	Date        string
	Label       string
	Tooltip     string
	Effort      float64
	EffortLabel string
	Level       int
	InRange     bool
}

type ContributionMonth struct {
	Label  string
	Column int
}

type ContributionData struct {
	Days        []ContributionDay
	Months      []ContributionMonth
	Weeks       int
	Year        int
	Levels      int
	StartLabel  string
	EndLabel    string
	MaxEffort   float64
	TotalEffort float64
}

type JobView struct {
	ID            int64
	TypeLabel     string
	Status        string
	StatusClass   string
	Attempts      int
	MaxAttempts   int
	NextRunAt     string
	UpdatedAt     string
	LastError     string
	CursorSummary string
}

type StravaConfig struct {
	ClientID        string
	ClientSecret    string
	AuthBaseURL     string
	RedirectURL     string
	InitialSyncDays int
	Clients         *strava.ClientFactory
	SessionSecret   string
}

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

// StaticHandler serves embedded static assets (leaflet, chart.js).
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

func NewServer(store *storage.Store, ingestor *ingest.Ingestor, mapAPI maps.API, overpass *maps.OverpassClient, stopOpts gps.StopOptions, stravaConfig StravaConfig) (*Server, error) {
	sessionSecret, err := sessionSecretBytes(stravaConfig.SessionSecret)
	if err != nil {
		return nil, err
	}

	funcs := template.FuncMap{
		"boolLabel": func(v bool) string {
			if v {
				return "On"
			}
			return "Off"
		},
		"seq": func(n int) []int {
			if n <= 0 {
				return nil
			}
			seq := make([]int, n)
			for i := range seq {
				seq[i] = i + 1
			}
			return seq
		},
	}
	landing, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/footer.html",
		"templates/landing.html",
	)
	if err != nil {
		return nil, err
	}
	profile, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/footer.html",
		"templates/profile.html",
	)
	if err != nil {
		return nil, err
	}
	settings, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/footer.html",
		"templates/settings.html",
	)
	if err != nil {
		return nil, err
	}
	admin, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/footer.html",
		"templates/admin.html",
	)
	if err != nil {
		return nil, err
	}
	activity, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/footer.html",
		"templates/activity.html",
	)
	if err != nil {
		return nil, err
	}
	return &Server{
		store:         store,
		ingestor:      ingestor,
		mapAPI:        mapAPI,
		overpass:      overpass,
		stopOpts:      stopOpts,
		strava:        stravaConfig,
		sessionSecret: sessionSecret,
		templates: map[string]*template.Template{
			"landing":  landing,
			"profile":  profile,
			"settings": settings,
			"admin":    admin,
			"activity": activity,
		},
	}, nil
}

func sessionSecretBytes(secret string) ([]byte, error) {
	if secret != "" {
		return []byte(secret), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (s *Server) getStravaInfo(ctx context.Context, userID int64) StravaInfo {
	if userID == 0 {
		return StravaInfo{}
	}
	token, err := s.store.GetStravaToken(ctx, userID)
	if err != nil {
		return StravaInfo{}
	}
	return StravaInfo{
		Connected:   true,
		AthleteID:   token.AthleteID,
		AthleteName: token.AthleteName,
	}
}

func (s *Server) userCount(ctx context.Context) int {
	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return 0
	}
	return count
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	_, ok := s.requireUserID(w, r)
	return ok
}

func (s *Server) requireUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, ok := s.currentUserID(r.Context(), r)
	if !ok {
		http.Redirect(w, r, "/connect/strava?next="+url.QueryEscape(sanitizedNextPath(r.URL.RequestURI(), "/activities/")), http.StatusFound)
		return 0, false
	}
	return userID, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return s.requireUserID(w, r)
}

func (s *Server) currentUserID(ctx context.Context, r *http.Request) (int64, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return 0, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, false
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	if !hmac.Equal(sigBytes, s.sign(payloadBytes)) {
		return 0, false
	}
	var payload struct {
		UserID  int64 `json:"user_id"`
		Expires int64 `json:"expires"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return 0, false
	}
	if payload.UserID == 0 || payload.Expires <= time.Now().Unix() {
		return 0, false
	}
	if _, err := s.store.GetStravaToken(ctx, payload.UserID); err != nil {
		return 0, false
	}
	return payload.UserID, true
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	payload, err := json.Marshal(struct {
		UserID  int64 `json:"user_id"`
		Expires int64 `json:"expires"`
	}{
		UserID:  userID,
		Expires: time.Now().Add(sessionDuration).Unix(),
	})
	if err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(s.sign(payload))
	setCookie(w, r, sessionCookieName, value, int(sessionDuration.Seconds()))
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	setCookie(w, r, sessionCookieName, "", -1)
}

func (s *Server) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func sanitizedNextPath(next, fallback string) string {
	trimmed := strings.TrimSpace(next)
	if trimmed == "" {
		return fallback
	}
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return fallback
	}
	return trimmed
}

func requestIsSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r),
		MaxAge:   maxAge,
	})
}

func randomToken(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	setCookie(w, r, name, "", -1)
}

func appendMessage(path, msg string) string {
	if msg == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "msg=" + url.QueryEscape(msg)
}

func readCookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) stravaClientForUser(ctx context.Context, userID int64) (*strava.Client, error) {
	if s.strava.Clients != nil {
		return s.strava.Clients.ClientForUser(ctx, userID)
	}
	if s.ingestor != nil && s.ingestor.Clients != nil {
		return s.ingestor.Clients.ClientForUser(ctx, userID)
	}
	if s.ingestor != nil && s.ingestor.Strava != nil {
		return s.ingestor.Strava, nil
	}
	return nil, fmt.Errorf("strava client not configured")
}

func (s *Server) Landing(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	userID, _ := s.currentUserID(r.Context(), r)
	data := LandingPageData{
		PageData: PageData{
			Title:      "weirdstats",
			Page:       "home",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Built for myself, friends, and random strangers. Not for scale, not for profit.",
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
	}
	if err := s.templates["landing"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) UsersCount(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/stats/users" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		http.Error(w, "failed to count users", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Users int `json:"users"`
	}{
		Users: count,
	})
}

func (s *Server) RulesMetadata(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/rules/metadata" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	registry := rules.DefaultRegistry()
	meta := rules.BuildMetadata(registry, rules.DefaultOperators())
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(meta); err != nil {
		http.Error(w, "failed to encode metadata", http.StatusInternalServerError)
	}
}

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
		view := ActivityView{
			ID:                activity.ID,
			Name:              activity.Name,
			Type:              activity.Type,
			StartTime:         activity.StartTime.Format("Jan 2, 2006 15:04"),
			Description:       activity.Description,
			StravaDescription: stravaDescription,
			Distance:          formatDistance(activity.Distance),
			Duration:          formatDuration(activity.MovingTime),
			HasStats:          activity.HasStats,
			StopCount:         activity.StopCount,
			StopTotal:         formatDuration(activity.StopTotalSeconds),
			LightStops:        activity.TrafficLightStopCount,
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

	var stopViews []StopView
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

	rideFact := rideSegmentFact{}
	coffeeFact := coffeeStopFact{}
	routeFact := routeHighlightFact{}
	roadFact := buildRoadCrossingFact(storedStops)
	if isRideType(activity.Type) && len(points) > 1 {
		rideFact = longestRideSegmentFact(activity.Type, points, s.stopOpts)
		if s.overpass != nil {
			var detectErr error
			coffeeFact, detectErr = detectCoffeeStopFact(r.Context(), activity.Type, points, s.overpass)
			if detectErr != nil {
				log.Printf("detail coffee stop detection failed for activity %d: %v", activityID, detectErr)
				coffeeFact = coffeeStopFact{}
			}
			routeFact, detectErr = detectRouteHighlightFact(r.Context(), points, s.overpass)
			if detectErr != nil {
				log.Printf("detail route highlight detection failed for activity %d: %v", activityID, detectErr)
				routeFact = routeHighlightFact{}
			}
		}
	}
	detectedFacts := buildActivityMapFacts(stopViews, points, rideFact, coffeeFact, routeFact, roadFact)

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

	recalculatedAt := ""
	stats, err := s.store.GetActivityStats(r.Context(), activityID)
	if err == nil {
		recalculatedAt = formatTimestamp(stats.UpdatedAt)
	} else if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "failed to load activity stats", http.StatusInternalServerError)
		return
	}

	view := ActivityView{
		ID:                activity.ID,
		Name:              activity.Name,
		Type:              activity.Type,
		StartTime:         activity.StartTime.Format("Jan 2, 2006 15:04"),
		Distance:          formatDistance(activity.Distance),
		Duration:          formatDuration(activity.MovingTime),
		HasStats:          len(stopViews) > 0,
		StopCount:         len(stopViews),
		StopTotal:         formatDuration(totalStopSeconds(stopViews)),
		LightStops:        countLightStops(stopViews),
		DetectedFactCount: len(detectedFacts),
		RoadCrossings:     countRoadCrossings(stopViews),
		RecalculatedAt:    recalculatedAt,
		FetchedAt:         formatTimestamp(activity.UpdatedAt),
	}
	enrichActivityView(&view, activity)

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
		RoutePointsJSON:   template.JS(pointsJSON),
		StopsJSON:         template.JS(stopsJSON),
		DetectedFactsJSON: template.JS(detectedFactsJSON),
		SpeedSeriesJSON:   template.JS(speedJSON),
		SpeedThreshold:    s.stopOpts.SpeedThreshold,
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
	roadPart := buildRoadCrossingPart(roadFact)
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
	if fact.Count <= 0 {
		return ""
	}
	roads := uniqueCrossingRoadNames(fact.Roads)
	if fact.Count == 1 {
		if len(roads) > 0 {
			return "Road crossing: " + roads[0]
		}
		return "1 road crossing"
	}
	if len(roads) > 0 {
		return fmt.Sprintf("%d road crossings: %s", fact.Count, strings.Join(roads, ", "))
	}
	return fmt.Sprintf("%d road crossings", fact.Count)
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
		}
		points = append(points, point)
	}
	return points
}

const (
	longestRideSegmentMinSpeedKPH = 15.0
	longestRideSegmentMinSpeedMPS = longestRideSegmentMinSpeedKPH / 3.6
	longestRideSegmentMinSlowTime = 5 * time.Second
	coffeeStopMinDuration         = 5 * time.Minute
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

func buildActivityMapFacts(stopViews []StopView, points []gps.Point, rideFact rideSegmentFact, coffeeFact coffeeStopFact, routeFact routeHighlightFact, roadFact roadCrossingFact) []ActivityMapFactView {
	facts := make([]ActivityMapFactView, 0, 6)

	if summary := trimFactPrefix(buildRideSegmentPart(rideFact), "Longest uninterrupted segment: "); summary != "" {
		fact := ActivityMapFactView{
			ID:      weirdStatsFactLongestSegment,
			Kind:    "segment",
			Title:   "Longest uninterrupted segment",
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
		summary := fmt.Sprintf("%d stops", len(stopViews))
		if total := totalStopSeconds(stopViews); total > 0 {
			summary += " · " + formatDuration(total) + " total"
		}
		facts = append(facts, ActivityMapFactView{
			ID:      weirdStatsFactStopSummary,
			Kind:    "collection",
			Title:   "Stop summary",
			Summary: summary,
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
			Summary: fmt.Sprintf("%d detected near traffic signals", len(lightStops)),
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
	if fact.StartIndex < 0 || fact.EndIndex >= len(points) || fact.StartIndex >= fact.EndIndex {
		return nil
	}
	path := make([]routePreviewPoint, 0, fact.EndIndex-fact.StartIndex+1)
	for _, point := range points[fact.StartIndex : fact.EndIndex+1] {
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

func (s *Server) Settings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/activities/settings" {
		http.NotFound(w, r)
		return
	}
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		s.handleSettingsPost(w, r, userID)
		return
	}

	registry := rules.DefaultRegistry()
	factSettings, err := s.loadWeirdStatsFactSettings(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to load fact settings", http.StatusInternalServerError)
		return
	}
	ruleRows, err := s.store.ListHideRules(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to load rules", http.StatusInternalServerError)
		return
	}
	var viewRules []SettingsRule
	for _, ruleRow := range ruleRows {
		description := ruleRow.Condition
		isLegacy := false
		if parsed, err := rules.ParseRuleJSON(ruleRow.Condition); err == nil {
			description = rules.Describe(parsed, registry)
		} else {
			isLegacy = true
		}
		viewRules = append(viewRules, SettingsRule{
			ID:          ruleRow.ID,
			Name:        ruleRow.Name,
			Description: description,
			Enabled:     ruleRow.Enabled,
			IsLegacy:    isLegacy,
		})
	}
	meta := rules.BuildMetadata(registry, rules.DefaultOperators())
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		metaJSON = []byte(`{\"metrics\":[],\"operators\":{}}`)
	}

	data := SettingsPageData{
		PageData: PageData{
			Title:      "Settings",
			Page:       "settings",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Rules and fact preferences are stored locally and applied when Weirdstats updates activities.",
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
		Facts:         buildSettingsFacts(factSettings),
		Rules:         viewRules,
		RulesMetaJSON: template.JS(string(metaJSON)),
	}
	if err := s.templates["settings"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) Admin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/admin" {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	userID, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		s.handleAdminPost(w, r, userID)
		return
	}

	queueCount, _ := s.store.CountQueue(r.Context())
	jobsView := s.buildJobViews(r.Context(), userID)
	activityJobsView := s.buildActivityJobViews(r.Context(), userID)

	data := AdminPageData{
		PageData: PageData{
			Title:      "Admin",
			Page:       "admin",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Admin actions are logged and may take time to complete.",
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
		QueueCount:   queueCount,
		Jobs:         jobsView,
		ActivityJobs: activityJobsView,
	}
	if err := s.templates["admin"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminPost(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/?msg=invalid+form", http.StatusFound)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "sync-latest":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		if err := s.enqueueLatestJob(r.Context(), userID); err != nil {
			http.Redirect(w, r, "/admin/?msg=sync+enqueue+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/admin/?msg=sync+queued+latest", http.StatusFound)
	case "sync-month":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		oneMonthAgo := time.Now().AddDate(0, -1, 0)
		if err := s.enqueueSyncJob(r.Context(), userID, oneMonthAgo); err != nil {
			http.Redirect(w, r, "/admin/?msg=sync+enqueue+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/admin/?msg=sync+queued+last+month", http.StatusFound)
	case "sync-year":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		oneYearAgo := time.Now().AddDate(-1, 0, 0)
		if err := s.enqueueSyncJob(r.Context(), userID, oneYearAgo); err != nil {
			http.Redirect(w, r, "/admin/?msg=sync+enqueue+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/admin/?msg=sync+queued+last+year", http.StatusFound)
	case "sync-all":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		if err := s.enqueueSyncJobWindow(r.Context(), userID, time.Unix(0, 0), 365); err != nil {
			http.Redirect(w, r, "/admin/?msg=sync+enqueue+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/admin/?msg=sync+queued+all", http.StatusFound)
	case "test-overpass":
		if s.overpass == nil {
			http.Redirect(w, r, "/admin/?msg=overpass+client+not+configured", http.StatusFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		// Midtown Manhattan test bbox: dense traffic lights and cafés.
		bbox := maps.BBox{
			South: 40.7568,
			West:  -73.9900,
			North: 40.7612,
			East:  -73.9820,
		}
		pois, err := s.overpass.FetchPOIs(ctx, bbox, true, true)
		if err != nil {
			http.Redirect(w, r, "/admin/?msg="+url.QueryEscape("overpass failed: "+err.Error()), http.StatusFound)
			return
		}
		msg := fmt.Sprintf("overpass ok: %d features in test bbox", len(pois))
		http.Redirect(w, r, "/admin/?msg="+url.QueryEscape(msg), http.StatusFound)
	case "clear-jobs":
		http.Redirect(w, r, "/admin/?msg=job+clearing+disabled+for+multi-user+safety", http.StatusFound)
	default:
		http.Redirect(w, r, "/admin/?msg=unknown+action", http.StatusFound)
	}
}

func (s *Server) ConnectStrava(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/connect/strava" {
		http.NotFound(w, r)
		return
	}
	if s.strava.ClientID == "" || s.strava.ClientSecret == "" {
		http.Error(w, "strava client not configured", http.StatusInternalServerError)
		return
	}

	redirectURL := s.strava.RedirectURL
	if redirectURL == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		redirectURL = fmt.Sprintf("%s://%s/connect/strava/callback", scheme, r.Host)
	}

	base := s.strava.AuthBaseURL
	if base == "" {
		base = "https://www.strava.com"
	}
	endpoint, err := url.JoinPath(base, "/oauth/authorize")
	if err != nil {
		http.Error(w, "failed to build oauth url", http.StatusInternalServerError)
		return
	}

	params := url.Values{}
	params.Set("client_id", s.strava.ClientID)
	params.Set("redirect_uri", redirectURL)
	params.Set("response_type", "code")
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "failed to initialize oauth state", http.StatusInternalServerError)
		return
	}
	next := sanitizedNextPath(r.URL.Query().Get("next"), "/activities/")
	setCookie(w, r, oauthStateCookieName, state, 10*60)
	setCookie(w, r, oauthNextCookieName, base64.RawURLEncoding.EncodeToString([]byte(next)), 10*60)
	params.Set("state", state)
	if r.URL.Query().Get("force") == "1" {
		params.Set("approval_prompt", "force")
	} else {
		params.Set("approval_prompt", "auto")
	}
	params.Set("scope", "read,activity:read_all,activity:write")

	http.Redirect(w, r, endpoint+"?"+params.Encode(), http.StatusFound)
}

func (s *Server) StravaCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/connect/strava/callback" {
		http.NotFound(w, r)
		return
	}
	expectedState := readCookieValue(r, oauthStateCookieName)
	nextEncoded := readCookieValue(r, oauthNextCookieName)
	clearCookie(w, r, oauthStateCookieName)
	clearCookie(w, r, oauthNextCookieName)
	next := "/activities/"
	if nextBytes, err := base64.RawURLEncoding.DecodeString(nextEncoded); err == nil {
		next = sanitizedNextPath(string(nextBytes), next)
	}
	if expectedState == "" || r.URL.Query().Get("state") != expectedState {
		http.Redirect(w, r, appendMessage("/", "invalid oauth state"), http.StatusFound)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Redirect(w, r, appendMessage("/", "strava authorization failed"), http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	token, err := strava.ExchangeAuthorizationCode(
		r.Context(),
		s.strava.AuthBaseURL,
		s.strava.ClientID,
		s.strava.ClientSecret,
		code,
		nil,
	)
	if err != nil {
		log.Printf("strava oauth exchange failed: %v", err)
		http.Redirect(w, r, appendMessage("/", "strava authorization failed"), http.StatusFound)
		return
	}
	userID := token.Athlete.ID
	if userID == 0 {
		http.Redirect(w, r, appendMessage("/", "strava token save failed"), http.StatusFound)
		return
	}
	_, err = s.store.GetStravaToken(r.Context(), userID)
	firstConnect := false
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("strava token lookup failed: %v", err)
		}
		firstConnect = true
	}
	athleteName := token.Athlete.FirstName
	if token.Athlete.LastName != "" {
		athleteName += " " + token.Athlete.LastName
	}
	if userID != 1 {
		legacy, err := s.store.GetStravaToken(r.Context(), 1)
		if err == nil && (legacy.AthleteID == 0 || legacy.AthleteID == userID) {
			if err := s.store.ReassignUserData(r.Context(), 1, userID); err != nil {
				log.Printf("legacy user migration failed: %v", err)
				http.Redirect(w, r, appendMessage("/", "strava token save failed"), http.StatusFound)
				return
			}
		}
	}
	log.Printf("Saving token: expires_at=%d (%v), athlete=%d %s",
		token.ExpiresAt, time.Unix(token.ExpiresAt, 0), token.Athlete.ID, athleteName)
	if err := s.store.UpsertStravaToken(r.Context(), storage.StravaToken{
		UserID:       userID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Unix(token.ExpiresAt, 0),
		AthleteID:    token.Athlete.ID,
		AthleteName:  athleteName,
	}); err != nil {
		log.Printf("strava token save failed: %v", err)
		http.Redirect(w, r, appendMessage("/", "strava token save failed"), http.StatusFound)
		return
	}
	if userID != 1 {
		if legacy, err := s.store.GetStravaToken(r.Context(), 1); err == nil && (legacy.AthleteID == 0 || legacy.AthleteID == userID) {
			_ = s.store.DeleteStravaToken(r.Context(), 1)
		}
	}
	if err := s.setSession(w, r, userID); err != nil {
		http.Redirect(w, r, appendMessage("/", "session creation failed"), http.StatusFound)
		return
	}
	if firstConnect {
		if s.ingestor == nil {
			log.Printf("strava connected; ingestor not configured, skipping initial sync")
		} else if s.strava.InitialSyncDays <= 0 {
			log.Printf("strava connected; initial sync disabled")
		} else {
			days := s.strava.InitialSyncDays
			log.Printf("strava connected; starting initial sync (%d days)", days)
			after := time.Now().AddDate(0, 0, -days)
			if err := s.enqueueSyncJob(r.Context(), userID, after); err != nil {
				log.Printf("initial sync enqueue failed: %v", err)
			}
		}
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func compactErrMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return "unknown error"
	}
	const max = 200
	if len(msg) > max {
		return msg[:max] + "..."
	}
	return msg
}

func compactForLog(raw string, max int) string {
	msg := strings.TrimSpace(raw)
	msg = strings.Join(strings.Fields(msg), " ")
	if max > 0 && len(msg) > max {
		return msg[:max] + "..."
	}
	return msg
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/activities/settings?msg=invalid+form", http.StatusFound)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "update-facts":
		settings := defaultWeirdStatsFactSettings()
		for _, def := range weirdStatsFactDefinitions {
			settings[def.ID] = r.FormValue("fact_"+def.ID) == "on"
		}
		if err := s.store.ReplaceUserFactPreferences(r.Context(), userID, buildUserFactPreferences(settings)); err != nil {
			http.Redirect(w, r, "/activities/settings?msg=fact+update+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/settings?msg=facts+updated", http.StatusFound)
	case "add-rule":
		name := strings.TrimSpace(r.FormValue("name"))
		condition := strings.TrimSpace(r.FormValue("condition"))
		enabled := r.FormValue("enabled") == "on"
		if name == "" || condition == "" {
			http.Redirect(w, r, "/activities/settings?msg=missing+rule+fields", http.StatusFound)
			return
		}
		parsedRule, err := rules.ParseRuleJSON(condition)
		if err != nil {
			detail := compactErrMessage(err)
			log.Printf("settings add-rule parse failed: name=%q enabled=%t err=%v json=%q", name, enabled, err, compactForLog(condition, 500))
			http.Redirect(w, r, "/activities/settings?msg="+url.QueryEscape("invalid rule json: "+detail), http.StatusFound)
			return
		}
		if err := rules.ValidateRule(parsedRule, rules.DefaultRegistry()); err != nil {
			detail := compactErrMessage(err)
			log.Printf("settings add-rule validation failed: name=%q enabled=%t err=%v json=%q", name, enabled, err, compactForLog(condition, 500))
			http.Redirect(w, r, "/activities/settings?msg="+url.QueryEscape("invalid rule definition: "+detail), http.StatusFound)
			return
		}
		normalized, err := json.Marshal(parsedRule)
		if err != nil {
			log.Printf("settings add-rule normalize failed: name=%q enabled=%t err=%v", name, enabled, err)
			http.Redirect(w, r, "/activities/settings?msg=rule+save+failed", http.StatusFound)
			return
		}
		condition = string(normalized)
		if _, err := s.store.CreateHideRule(r.Context(), storage.HideRule{
			UserID:    userID,
			Name:      name,
			Condition: condition,
			Enabled:   enabled,
		}); err != nil {
			log.Printf("settings add-rule store failed: name=%q enabled=%t err=%v", name, enabled, err)
			http.Redirect(w, r, "/activities/settings?msg=rule+save+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/settings?msg=rule+added", http.StatusFound)
	case "toggle-rule":
		idValue := r.FormValue("rule_id")
		enabled := r.FormValue("enabled") == "on"
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/activities/settings?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.UpdateHideRuleEnabledForUser(r.Context(), userID, ruleID, enabled); err != nil {
			http.Redirect(w, r, "/activities/settings?msg=rule+update+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/settings?msg=rule+updated", http.StatusFound)
	case "delete-rule":
		idValue := r.FormValue("rule_id")
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/activities/settings?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.DeleteHideRuleForUser(r.Context(), userID, ruleID); err != nil {
			http.Redirect(w, r, "/activities/settings?msg=rule+delete+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/settings?msg=rule+deleted", http.StatusFound)
	case "log-out":
		s.clearSession(w, r)
		http.Redirect(w, r, "/?msg=signed+out", http.StatusFound)
	case "disconnect-strava":
		if err := s.store.DeleteStravaToken(r.Context(), userID); err != nil {
			http.Redirect(w, r, "/activities/settings?msg=disconnect+failed", http.StatusFound)
			return
		}
		s.clearSession(w, r)
		http.Redirect(w, r, "/?msg=strava+disconnected", http.StatusFound)
	case "delete-account":
		if strings.TrimSpace(r.FormValue("confirm")) != "delete" {
			http.Redirect(w, r, "/activities/settings?msg=confirm+delete+account", http.StatusFound)
			return
		}
		if err := s.store.DeleteUserData(r.Context(), userID); err != nil {
			http.Redirect(w, r, "/activities/settings?msg=delete+failed", http.StatusFound)
			return
		}
		s.clearSession(w, r)
		http.Redirect(w, r, "/?msg=account+deleted", http.StatusFound)
	default:
		http.Redirect(w, r, "/activities/settings?msg=unknown+action", http.StatusFound)
	}
}

func (s *Server) enqueueSyncJob(ctx context.Context, userID int64, after time.Time) error {
	return s.enqueueSyncJobWindow(ctx, userID, after, 1)
}

func (s *Server) enqueueSyncJobWindow(ctx context.Context, userID int64, after time.Time, windowDays int) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}
	if windowDays <= 0 {
		windowDays = 1
	}
	payload := jobs.SyncSincePayload{
		UserID:     userID,
		AfterUnix:  after.Unix(),
		PerPage:    100,
		WindowDays: windowDays,
	}
	cursor := jobs.SyncSinceCursor{Page: 1}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cursorJSON, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	_, err = s.store.CreateJob(ctx, storage.Job{
		Type:        jobs.JobTypeSyncActivitiesSince,
		Payload:     string(payloadJSON),
		Cursor:      string(cursorJSON),
		MaxAttempts: 1000,
		NextRunAt:   time.Now(),
	})
	return err
}

func (s *Server) enqueueLatestJob(ctx context.Context, userID int64) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}
	payload := jobs.SyncLatestPayload{UserID: userID}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cursorJSON, err := json.Marshal(jobs.SyncLatestCursor{})
	if err != nil {
		return err
	}
	_, err = s.store.CreateJob(ctx, storage.Job{
		Type:        jobs.JobTypeSyncLatest,
		Payload:     string(payloadJSON),
		Cursor:      string(cursorJSON),
		MaxAttempts: 1000,
		NextRunAt:   time.Now(),
	})
	return err
}

func (s *Server) buildJobViews(ctx context.Context, userID int64) []JobView {
	jobsList, err := s.store.ListJobsExcludingType(ctx, jobs.JobTypeProcessActivity, 20)
	if err != nil {
		log.Printf("jobs load failed: %v", err)
		return nil
	}
	return s.buildJobViewsFromList(ctx, jobsList, userID)
}

func (s *Server) buildActivityJobViews(ctx context.Context, userID int64) []JobView {
	jobsList, err := s.store.ListJobsByType(ctx, jobs.JobTypeProcessActivity, 20)
	if err != nil {
		log.Printf("activity jobs load failed: %v", err)
		return nil
	}
	return s.buildJobViewsFromList(ctx, jobsList, userID)
}

func (s *Server) buildJobViewsFromList(ctx context.Context, jobsList []storage.Job, userID int64) []JobView {
	var views []JobView
	for _, job := range jobsList {
		if !s.jobBelongsToUser(ctx, job, userID) {
			continue
		}
		view := JobView{
			ID:            job.ID,
			TypeLabel:     jobTypeLabel(job),
			Status:        job.Status,
			StatusClass:   jobStatusClass(job.Status),
			Attempts:      job.Attempts,
			MaxAttempts:   job.MaxAttempts,
			NextRunAt:     formatTimestamp(job.NextRunAt),
			UpdatedAt:     formatTimestamp(job.UpdatedAt),
			LastError:     job.LastError,
			CursorSummary: jobCursorSummary(job),
		}
		views = append(views, view)
	}
	return views
}

func (s *Server) jobBelongsToUser(ctx context.Context, job storage.Job, userID int64) bool {
	switch job.Type {
	case jobs.JobTypeSyncLatest:
		var payload jobs.SyncLatestPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return false
		}
		return payload.UserID == userID
	case jobs.JobTypeSyncActivitiesSince:
		var payload jobs.SyncSincePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return false
		}
		return payload.UserID == userID
	case jobs.JobTypeProcessActivity, jobs.JobTypeApplyActivityRules:
		var payload jobs.ProcessActivityPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return false
		}
		if payload.UserID != 0 {
			return payload.UserID == userID
		}
		activity, err := s.store.GetActivity(ctx, payload.ActivityID)
		if err != nil {
			return false
		}
		return activity.UserID == userID
	default:
		return false
	}
}

func jobTypeLabel(job storage.Job) string {
	switch job.Type {
	case jobs.JobTypeSyncLatest:
		return "Sync latest"
	case jobs.JobTypeSyncActivitiesSince:
		var payload jobs.SyncSincePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err == nil {
			if payload.AfterUnix > 0 {
				return fmt.Sprintf("Sync since %s", time.Unix(payload.AfterUnix, 0).Format("Jan 2, 2006"))
			}
			return "Sync all"
		}
		return "Sync since"
	case jobs.JobTypeProcessActivity:
		var payload jobs.ProcessActivityPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err == nil && payload.ActivityID > 0 {
			return fmt.Sprintf("Process activity %d", payload.ActivityID)
		}
		return "Process activity"
	case jobs.JobTypeApplyActivityRules:
		var payload jobs.ProcessActivityPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err == nil && payload.ActivityID > 0 {
			return fmt.Sprintf("Apply activity %d", payload.ActivityID)
		}
		return "Apply activity"
	default:
		return job.Type
	}
}

func jobStatusClass(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "running":
		return "running"
	case "failed":
		return "failed"
	case "retry":
		return "retry"
	case "queued":
		return "queued"
	default:
		return "queued"
	}
}

func jobCursorSummary(job storage.Job) string {
	switch job.Type {
	case jobs.JobTypeSyncActivitiesSince:
		var cursor jobs.SyncSinceCursor
		if err := json.Unmarshal([]byte(job.Cursor), &cursor); err != nil {
			return ""
		}
		if cursor.Page <= 0 {
			cursor.Page = 1
		}
		windowStart := ""
		windowEnd := ""
		if cursor.WindowStartUnix > 0 {
			windowStart = formatTimestamp(time.Unix(cursor.WindowStartUnix, 0))
		}
		if cursor.WindowEndUnix > 0 {
			windowEnd = formatTimestamp(time.Unix(cursor.WindowEndUnix, 0))
		}
		if windowStart != "" || windowEnd != "" {
			return fmt.Sprintf("window: %s - %s, page %d, enqueued %d", windowStart, windowEnd, cursor.Page, cursor.Enqueued)
		}
		return fmt.Sprintf("cursor: page %d, enqueued %d", cursor.Page, cursor.Enqueued)
	case jobs.JobTypeSyncLatest:
		var cursor jobs.SyncLatestCursor
		if err := json.Unmarshal([]byte(job.Cursor), &cursor); err != nil {
			return ""
		}
		return fmt.Sprintf("cursor: enqueued %d", cursor.Enqueued)
	default:
		return ""
	}
}

type ActivityDownload struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	StartTime   time.Time       `json:"start_time"`
	Description string          `json:"description"`
	Distance    float64         `json:"distance"`
	MovingTime  int             `json:"moving_time"`
	Points      []PointDownload `json:"points"`
}

type PointDownload struct {
	Lat   float64   `json:"lat"`
	Lon   float64   `json:"lon"`
	Time  time.Time `json:"time"`
	Speed float64   `json:"speed"`
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

func formatDuration(totalSeconds int) string {
	if totalSeconds <= 0 {
		return "0m"
	}
	duration := time.Duration(totalSeconds) * time.Second
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format("Jan 2, 2006 15:04")
}

func formatDistance(meters float64) string {
	if meters <= 0 {
		return ""
	}
	km := meters / 1000
	if km >= 10 {
		return fmt.Sprintf("%.1f km", km)
	}
	return fmt.Sprintf("%.2f km", km)
}

func (s *Server) buildContributionData(ctx context.Context, userID int64, now time.Time) ContributionData {
	return s.buildContributionDataForYear(ctx, userID, now.Year(), now)
}

func (s *Server) buildContributionDataForYear(ctx context.Context, userID int64, year int, now time.Time) ContributionData {
	loc := time.Local
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
	end := time.Date(year, time.December, 31, 0, 0, 0, 0, loc)
	rangeEnd := end
	if year == now.Year() {
		rangeEnd = time.Date(year, now.Month(), now.Day(), 0, 0, 0, 0, loc)
	}
	startGrid := start
	for startGrid.Weekday() != time.Monday {
		startGrid = startGrid.AddDate(0, 0, -1)
	}
	endGrid := end
	for endGrid.Weekday() != time.Sunday {
		endGrid = endGrid.AddDate(0, 0, 1)
	}

	activities, err := s.store.ListActivityTimes(ctx, userID, startGrid, rangeEnd.AddDate(0, 0, 1))
	if err != nil {
		log.Printf("contrib load failed: %v", err)
	}

	effortByDay := make(map[string]float64)
	for _, activity := range activities {
		if activity.MovingTime <= 0 {
			continue
		}
		dayKey := activity.StartTime.In(loc).Format("2006-01-02")
		effort := 0.0
		if activity.EffortVersion > 0 && activity.EffortScore > 0 {
			effort = activity.EffortScore / 60.0
		} else {
			effort = float64(activity.MovingTime) / 3600
		}
		if effort <= 0 {
			continue
		}
		effortByDay[dayKey] += effort
	}

	maxEffort := 0.0
	totalEffort := 0.0
	for day := start; !day.After(rangeEnd); day = day.AddDate(0, 0, 1) {
		effort := effortByDay[day.Format("2006-01-02")]
		if effort > maxEffort {
			maxEffort = effort
		}
		totalEffort += effort
	}

	var days []ContributionDay
	var months []ContributionMonth
	weekIndex := 0
	for weekStart := startGrid; !weekStart.After(endGrid); weekStart = weekStart.AddDate(0, 0, 7) {
		weekIndex++
		for i := 0; i < 7; i++ {
			day := weekStart.AddDate(0, 0, i)
			if day.Before(start) || day.After(end) {
				continue
			}
			if day.Day() == 1 {
				months = append(months, ContributionMonth{
					Label:  day.Format("Jan"),
					Column: weekIndex,
				})
				break
			}
		}
		for i := 0; i < 7; i++ {
			day := weekStart.AddDate(0, 0, i)
			inYear := !day.Before(start) && !day.After(end)
			inRange := !day.Before(start) && !day.After(rangeEnd)
			dateKey := day.Format("2006-01-02")
			effort := 0.0
			if inRange {
				effort = effortByDay[dateKey]
			}
			level := 0
			if inRange {
				level = contributionLevel(effort)
			}
			effortLabel := ""
			if inRange {
				effortLabel = formatEffort(effort)
			}
			days = append(days, ContributionDay{
				Date:        dateKey,
				Label:       day.Format("Jan 2, 2006"),
				Tooltip:     contributionTooltip(day, inRange, inYear, effortLabel, year),
				Effort:      effort,
				EffortLabel: effortLabel,
				Level:       level,
				InRange:     inRange,
			})
		}
	}

	weeks := weekIndex
	if weeks < 1 {
		weeks = 1
	}

	return ContributionData{
		Days:        days,
		Months:      months,
		Weeks:       weeks,
		Year:        year,
		Levels:      contributionMaxLevel,
		StartLabel:  start.Format("Jan 2, 2006"),
		EndLabel:    end.Format("Jan 2, 2006"),
		MaxEffort:   maxEffort,
		TotalEffort: totalEffort,
	}
}

func contributionTooltip(day time.Time, inRange, inYear bool, effortLabel string, year int) string {
	label := day.Format("Mon, Jan 2, 2006")
	switch {
	case inRange:
		if effortLabel == "" {
			return label
		}
		return fmt.Sprintf("%s · %s", label, effortLabel)
	case inYear:
		return fmt.Sprintf("%s · Future day", label)
	default:
		return fmt.Sprintf("%s · Outside %d", label, year)
	}
}

func buildRoutePreviewPath(points []storage.ActivityRoutePoint, width, height, padding float64) (string, float64, float64, float64, float64, bool) {
	if len(points) < 2 {
		return "", 0, 0, 0, 0, false
	}
	if width <= (padding*2) || height <= (padding*2) {
		return "", 0, 0, 0, 0, false
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

	latSpan := maxLat - minLat
	lonSpan := maxLon - minLon
	if latSpan == 0 && lonSpan == 0 {
		return "", 0, 0, 0, 0, false
	}

	innerWidth := width - (padding * 2)
	innerHeight := height - (padding * 2)
	if latSpan == 0 {
		latSpan = 1
	}
	if lonSpan == 0 {
		lonSpan = 1
	}

	scale := math.Min(innerWidth/lonSpan, innerHeight/latSpan)
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return "", 0, 0, 0, 0, false
	}

	routeWidth := (maxLon - minLon) * scale
	routeHeight := (maxLat - minLat) * scale
	offsetX := padding + ((innerWidth - routeWidth) / 2)
	offsetY := padding + ((innerHeight - routeHeight) / 2)

	var path strings.Builder
	path.Grow(len(points) * 14)

	pointCount := 0
	var startX, startY, endX, endY float64
	for _, point := range points {
		x := offsetX
		if maxLon != minLon {
			x += (point.Lon - minLon) * scale
		}
		y := offsetY
		if maxLat != minLat {
			y += (maxLat - point.Lat) * scale
		}

		if pointCount == 0 {
			fmt.Fprintf(&path, "M %.2f %.2f", x, y)
			startX, startY = x, y
		} else {
			fmt.Fprintf(&path, " L %.2f %.2f", x, y)
		}
		endX, endY = x, y
		pointCount++
	}

	if pointCount < 2 || (startX == endX && startY == endY) {
		return "", 0, 0, 0, 0, false
	}
	return path.String(), startX, startY, endX, endY, true
}

func enrichActivityView(view *ActivityView, activity storage.Activity) {
	view.TypeLabel = activityTypeLabel(activity.Type)
	view.TypeClass = activityTypeClass(activity.Type)
	view.IsHidden = isActivityHidden(activity)
	view.FeedMuted = activity.HideFromHome
	view.DistanceValue, view.DistanceUnit = formatDistanceParts(activity.Distance)
	view.PaceLabel, view.PaceValue, view.PaceUnit = formatPaceOrSpeed(activity.Type, activity.Distance, activity.MovingTime)
	view.PowerValue, view.PowerUnit, view.HasPower = formatPower(activity.AveragePower)
}

func formatDistanceParts(meters float64) (string, string) {
	if meters <= 0 {
		return "—", ""
	}
	km := meters / 1000
	if km >= 10 {
		return fmt.Sprintf("%.1f", km), "km"
	}
	return fmt.Sprintf("%.2f", km), "km"
}

func formatPaceOrSpeed(activityType string, meters float64, seconds int) (string, string, string) {
	if isPaceType(activityType) {
		value, unit := formatPace(meters, seconds)
		return "Pace", value, unit
	}
	value, unit := formatSpeed(meters, seconds)
	return "Avg speed", value, unit
}

func formatPace(meters float64, seconds int) (string, string) {
	if meters <= 0 || seconds <= 0 {
		return "—", ""
	}
	paceSeconds := int(math.Round(float64(seconds) / (meters / 1000)))
	minutes := paceSeconds / 60
	remaining := paceSeconds % 60
	return fmt.Sprintf("%d:%02d", minutes, remaining), "/km"
}

func formatSpeed(meters float64, seconds int) (string, string) {
	if meters <= 0 || seconds <= 0 {
		return "—", ""
	}
	hours := float64(seconds) / 3600
	speed := (meters / 1000) / hours
	return fmt.Sprintf("%.1f", speed), "km/h"
}

func formatPower(watts float64) (string, string, bool) {
	if watts <= 0 {
		return "—", "", false
	}
	return fmt.Sprintf("%.0f", math.Round(watts)), "W", true
}

func formatEffort(effort float64) string {
	if effort <= 0 {
		return "No effort"
	}
	if effort < 10 {
		return fmt.Sprintf("Effort %.1f h", effort)
	}
	return fmt.Sprintf("Effort %.0f h", effort)
}

const contributionMaxLevel = 11

func contributionLevel(effort float64) int {
	if effort <= 0 {
		return 0
	}
	switch {
	case effort < 1:
		return 1
	case effort < 2:
		return 2
	case effort < 3:
		return 3
	case effort < 4:
		return 4
	case effort < 5:
		return 5
	case effort < 6:
		return 6
	case effort < 7:
		return 7
	case effort < 8:
		return 8
	case effort < 9:
		return 9
	case effort < 10:
		return 10
	default:
		return 11
	}
}

func activityTypeClass(activityType string) string {
	t := strings.ToLower(activityType)
	switch {
	case strings.Contains(t, "ride"):
		return "ride"
	case strings.Contains(t, "run"):
		return "run"
	case strings.Contains(t, "swim"):
		return "swim"
	case t == "walk" || t == "hike":
		return "walk"
	case strings.Contains(t, "workout") || strings.Contains(t, "training") || t == "yoga":
		return "workout"
	default:
		return "other"
	}
}

func activityTypeLabel(activityType string) string {
	if activityType == "" {
		return "Activity"
	}
	return splitCamelCase(activityType)
}

func splitCamelCase(input string) string {
	runes := []rune(input)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		if r == '_' || r == '-' {
			b.WriteRune(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isPaceType(activityType string) bool {
	t := strings.ToLower(activityType)
	if strings.Contains(t, "run") {
		return true
	}
	switch t {
	case "walk", "hike":
		return true
	default:
		return false
	}
}

func isActivityHidden(activity storage.Activity) bool {
	if activity.HiddenByRule {
		return true
	}
	if activity.HideFromHome || activity.IsPrivate {
		return true
	}
	if strings.EqualFold(activity.Visibility, "only_me") || strings.EqualFold(activity.Visibility, "private") {
		return true
	}
	return false
}
