package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/maps"
	"weirdstats/internal/rules"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/**
var staticFS embed.FS

//go:embed static/*
var StaticFS embed.FS

type Server struct {
	store     *storage.Store
	ingestor  *ingest.Ingestor
	mapAPI    maps.API
	overpass  *maps.OverpassClient
	stopOpts  gps.StopOptions
	templates map[string]*template.Template
	strava    StravaConfig
}

type ActivityView struct {
	ID             int64
	Name           string
	Type           string
	TypeLabel      string
	TypeClass      string
	StartTime      string
	Description    string
	Distance       string
	DistanceValue  string
	DistanceUnit   string
	Duration       string
	PaceLabel      string
	PaceValue      string
	PaceUnit       string
	PowerValue     string
	PowerUnit      string
	HasPower       bool
	HasStats       bool
	StopCount      int
	StopTotal      string
	LightStops     int
	RoadCrossings  int
	RecalculatedAt string
	FetchedAt      string
	IsHidden       bool
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

type ActivityDetailData struct {
	PageData
	Activity        ActivityView
	Stops           []StopView
	RoutePointsJSON template.JS
	StopsJSON       template.JS
	SpeedSeriesJSON template.JS
	SpeedThreshold  float64
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
	Contributions ContributionData
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
	Rules         []SettingsRule
	RulesMetaJSON template.JS
}

type AdminPageData struct {
	PageData
	QueueCount int
}

type ContributionDay struct {
	Date       string
	Label      string
	Hours      float64
	HoursLabel string
	Level      int
	InRange    bool
}

type ContributionMonth struct {
	Label  string
	Column int
}

type ContributionData struct {
	Days       []ContributionDay
	Months     []ContributionMonth
	Weeks      int
	StartLabel string
	EndLabel   string
	MaxHours   float64
	TotalHours float64
}

type StravaConfig struct {
	ClientID        string
	ClientSecret    string
	AuthBaseURL     string
	RedirectURL     string
	InitialSyncDays int
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
	funcs := template.FuncMap{
		"boolLabel": func(v bool) string {
			if v {
				return "On"
			}
			return "Off"
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
		store:    store,
		ingestor: ingestor,
		mapAPI:   mapAPI,
		overpass: overpass,
		stopOpts: stopOpts,
		strava:   stravaConfig,
		templates: map[string]*template.Template{
			"landing":  landing,
			"profile":  profile,
			"settings": settings,
			"admin":    admin,
			"activity": activity,
		},
	}, nil
}

func (s *Server) getStravaInfo(ctx context.Context) StravaInfo {
	token, err := s.store.GetStravaToken(ctx, 1)
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
	_, err := s.store.GetStravaToken(r.Context(), 1)
	if err != nil {
		http.Error(w, "Unauthorized - Please connect Strava first", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	token, err := s.store.GetStravaToken(r.Context(), 1)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	// For now, user 1 is always admin. In future, check against admin athlete IDs.
	if token.UserID != 1 {
		http.Error(w, "Forbidden - Admin access required", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) Landing(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := LandingPageData{
		PageData: PageData{
			Title:      "weirdstats",
			Page:       "home",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Built for myself, friends, and random strangers. Not for scale, not for profit.",
			Strava:     s.getStravaInfo(r.Context()),
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

func (s *Server) Profile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/profile" {
		http.Redirect(w, r, "/profile/", http.StatusFound)
		return
	}
	if r.URL.Path != "/profile/" {
		http.NotFound(w, r)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	activities, err := s.store.ListActivitiesWithStats(r.Context(), 1, 100)
	if err != nil {
		http.Error(w, "failed to load activities", http.StatusInternalServerError)
		return
	}
	var views []ActivityView
	for _, activity := range activities {
		view := ActivityView{
			ID:          activity.ID,
			Name:        activity.Name,
			Type:        activity.Type,
			StartTime:   activity.StartTime.Format("Jan 2, 2006 15:04"),
			Description: activity.Description,
			Distance:    formatDistance(activity.Distance),
			Duration:    formatDuration(activity.MovingTime),
			HasStats:    activity.HasStats,
			StopCount:   activity.StopCount,
			StopTotal:   formatDuration(activity.StopTotalSeconds),
			LightStops:  activity.TrafficLightStopCount,
		}
		enrichActivityView(&view, activity.Activity)
		views = append(views, view)
	}
	contrib := s.buildContributionData(r.Context(), time.Now())
	data := ProfilePageData{
		PageData: PageData{
			Title:      "Profile",
			Page:       "profile",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Tip: the worker runs in the background and fills in stats after ingest.",
			Strava:     s.getStravaInfo(r.Context()),
			UserCount:  s.userCount(r.Context()),
		},
		Activities:    views,
		Contributions: contrib,
	}
	if err := s.templates["profile"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) ActivityDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(w, r) {
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

	activity, err := s.store.GetActivity(r.Context(), activityID)
	if err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if activity.UserID != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
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
		ID:             activity.ID,
		Name:           activity.Name,
		Type:           activity.Type,
		StartTime:      activity.StartTime.Format("Jan 2, 2006 15:04"),
		Distance:       formatDistance(activity.Distance),
		Duration:       formatDuration(activity.MovingTime),
		HasStats:       len(stopViews) > 0,
		StopCount:      len(stopViews),
		StopTotal:      formatDuration(totalStopSeconds(stopViews)),
		LightStops:     countLightStops(stopViews),
		RoadCrossings:  countRoadCrossings(stopViews),
		RecalculatedAt: recalculatedAt,
		FetchedAt:      formatTimestamp(activity.UpdatedAt),
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
			Strava:     s.getStravaInfo(r.Context()),
			UserCount:  s.userCount(r.Context()),
		},
		Activity:        view,
		Stops:           stopViews,
		RoutePointsJSON: template.JS(pointsJSON),
		StopsJSON:       template.JS(stopsJSON),
		SpeedSeriesJSON: template.JS(speedJSON),
		SpeedThreshold:  s.stopOpts.SpeedThreshold,
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
	s.ActivityDetail(w, r)
}

func (s *Server) RefreshActivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(w, r) {
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

	activity, err := s.store.GetActivity(r.Context(), activityID)
	if err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}
	if activity.UserID != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.store.EnqueueActivity(r.Context(), activityID); err != nil {
		http.Error(w, "failed to enqueue activity", http.StatusInternalServerError)
		return
	}

	redirectURL := r.Header.Get("Referer")
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("/activity/%d", activityID)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func totalStopSeconds(stops []StopView) int {
	total := 0
	for _, s := range stops {
		total += s.DurationSeconds
	}
	return total
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
	if r.URL.Path != "/profile/settings" {
		http.NotFound(w, r)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if r.Method == http.MethodPost {
		s.handleSettingsPost(w, r)
		return
	}

	registry := rules.DefaultRegistry()
	ruleRows, err := s.store.ListHideRules(r.Context(), 1)
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
			FooterText: "Rules are stored locally and applied when the processing pipeline runs.",
			Strava:     s.getStravaInfo(r.Context()),
			UserCount:  s.userCount(r.Context()),
		},
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
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method == http.MethodPost {
		s.handleAdminPost(w, r)
		return
	}

	queueCount, _ := s.store.CountQueue(r.Context())

	data := AdminPageData{
		PageData: PageData{
			Title:      "Admin",
			Page:       "admin",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Admin actions are logged and may take time to complete.",
			Strava:     s.getStravaInfo(r.Context()),
			UserCount:  s.userCount(r.Context()),
		},
		QueueCount: queueCount,
	}
	if err := s.templates["admin"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminPost(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
		go func() {
			count, err := s.ingestor.SyncLatestActivity(context.Background())
			if err != nil {
				log.Printf("sync latest failed: %v", err)
			} else {
				log.Printf("sync latest completed: %d activity", count)
			}
		}()
		http.Redirect(w, r, "/admin/?msg=fetching+latest+started", http.StatusFound)
	case "sync-month":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		go func() {
			oneMonthAgo := time.Now().AddDate(0, -1, 0)
			count, err := s.ingestor.SyncActivitiesSince(context.Background(), oneMonthAgo)
			if err != nil {
				log.Printf("sync month failed after %d: %v", count, err)
			} else {
				log.Printf("sync month completed: %d activities", count)
			}
		}()
		http.Redirect(w, r, "/admin/?msg=fetching+last+month+started", http.StatusFound)
	case "sync-year":
		if s.ingestor == nil {
			http.Redirect(w, r, "/admin/?msg=sync+not+configured", http.StatusFound)
			return
		}
		go func() {
			oneYearAgo := time.Now().AddDate(-1, 0, 0)
			count, err := s.ingestor.SyncActivitiesSince(context.Background(), oneYearAgo)
			if err != nil {
				log.Printf("sync year failed after %d: %v", count, err)
			} else {
				log.Printf("sync year completed: %d activities", count)
			}
		}()
		http.Redirect(w, r, "/admin/?msg=fetching+last+year+started", http.StatusFound)
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
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Redirect(w, r, "/profile/settings?msg=strava+authorization+failed", http.StatusFound)
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
		http.Redirect(w, r, "/profile/settings?msg=strava+authorization+failed", http.StatusFound)
		return
	}
	existing, err := s.store.GetStravaToken(r.Context(), 1)
	firstConnect := false
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("strava token lookup failed: %v", err)
		}
		firstConnect = true
	} else if existing.AthleteID == 0 && existing.AthleteName == "" {
		firstConnect = true
	}
	athleteName := token.Athlete.FirstName
	if token.Athlete.LastName != "" {
		athleteName += " " + token.Athlete.LastName
	}
	log.Printf("Saving token: expires_at=%d (%v), athlete=%d %s",
		token.ExpiresAt, time.Unix(token.ExpiresAt, 0), token.Athlete.ID, athleteName)
	if err := s.store.UpsertStravaToken(r.Context(), storage.StravaToken{
		UserID:       1,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Unix(token.ExpiresAt, 0),
		AthleteID:    token.Athlete.ID,
		AthleteName:  athleteName,
	}); err != nil {
		log.Printf("strava token save failed: %v", err)
		http.Redirect(w, r, "/profile/settings?msg=strava+token+save+failed", http.StatusFound)
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
			go func() {
				after := time.Now().AddDate(0, 0, -days)
				count, err := s.ingestor.SyncActivitiesSince(context.Background(), after)
				if err != nil {
					log.Printf("initial sync failed: %v", err)
				} else {
					log.Printf("initial sync completed: %d activities", count)
				}
			}()
		}
	}
	http.Redirect(w, r, "/profile/?msg=strava+connected", http.StatusFound)
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/profile/settings?msg=invalid+form", http.StatusFound)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "add-rule":
		name := strings.TrimSpace(r.FormValue("name"))
		condition := strings.TrimSpace(r.FormValue("condition"))
		enabled := r.FormValue("enabled") == "on"
		if name == "" || condition == "" {
			http.Redirect(w, r, "/profile/settings?msg=missing+rule+fields", http.StatusFound)
			return
		}
		parsedRule, err := rules.ParseRuleJSON(condition)
		if err != nil {
			http.Redirect(w, r, "/profile/settings?msg=invalid+rule+json", http.StatusFound)
			return
		}
		if err := rules.ValidateRule(parsedRule, rules.DefaultRegistry()); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=invalid+rule+definition", http.StatusFound)
			return
		}
		normalized, err := json.Marshal(parsedRule)
		if err != nil {
			http.Redirect(w, r, "/profile/settings?msg=rule+save+failed", http.StatusFound)
			return
		}
		condition = string(normalized)
		if _, err := s.store.CreateHideRule(r.Context(), storage.HideRule{
			UserID:    1,
			Name:      name,
			Condition: condition,
			Enabled:   enabled,
		}); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=rule+save+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/profile/settings?msg=rule+added", http.StatusFound)
	case "toggle-rule":
		idValue := r.FormValue("rule_id")
		enabled := r.FormValue("enabled") == "on"
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/profile/settings?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.UpdateHideRuleEnabled(r.Context(), ruleID, enabled); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=rule+update+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/profile/settings?msg=rule+updated", http.StatusFound)
	case "delete-rule":
		idValue := r.FormValue("rule_id")
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/profile/settings?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.DeleteHideRule(r.Context(), ruleID); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=rule+delete+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/profile/settings?msg=rule+deleted", http.StatusFound)
	case "sign-out":
		if err := s.store.DeleteStravaToken(r.Context(), 1); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=sign+out+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/?msg=signed+out", http.StatusFound)
	case "delete-account":
		if strings.TrimSpace(r.FormValue("confirm")) != "delete" {
			http.Redirect(w, r, "/profile/settings?msg=confirm+delete+account", http.StatusFound)
			return
		}
		if err := s.store.DeleteUserData(r.Context(), 1); err != nil {
			http.Redirect(w, r, "/profile/settings?msg=delete+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/?msg=account+deleted", http.StatusFound)
	default:
		http.Redirect(w, r, "/profile/settings?msg=unknown+action", http.StatusFound)
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
	if !s.requireAuth(w, r) {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	idStr = strings.TrimSuffix(idStr, "/download")
	activityID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || activityID == 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	activity, err := s.store.GetActivity(r.Context(), activityID)
	if err != nil {
		http.Error(w, "activity not found", http.StatusNotFound)
		return
	}

	// Verify user owns this activity
	if activity.UserID != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
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

func (s *Server) buildContributionData(ctx context.Context, now time.Time) ContributionData {
	loc := time.Local
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -364)
	startGrid := start
	for startGrid.Weekday() != time.Sunday {
		startGrid = startGrid.AddDate(0, 0, -1)
	}
	endGrid := end
	for endGrid.Weekday() != time.Saturday {
		endGrid = endGrid.AddDate(0, 0, 1)
	}

	activities, err := s.store.ListActivityTimes(ctx, 1, startGrid, endGrid.AddDate(0, 0, 1))
	if err != nil {
		log.Printf("contrib load failed: %v", err)
	}

	hoursByDay := make(map[string]float64)
	for _, activity := range activities {
		if activity.MovingTime <= 0 {
			continue
		}
		dayKey := activity.StartTime.In(loc).Format("2006-01-02")
		hoursByDay[dayKey] += float64(activity.MovingTime) / 3600
	}

	maxHours := 0.0
	totalHours := 0.0
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		hours := hoursByDay[day.Format("2006-01-02")]
		if hours > maxHours {
			maxHours = hours
		}
		totalHours += hours
	}

	var days []ContributionDay
	var months []ContributionMonth
	lastMonth := time.Month(0)
	dayIndex := 0
	for day := startGrid; !day.After(endGrid); day = day.AddDate(0, 0, 1) {
		inRange := !day.Before(start) && !day.After(end)
		dateKey := day.Format("2006-01-02")
		hours := 0.0
		if inRange {
			hours = hoursByDay[dateKey]
		}
		level := 0
		if inRange {
			level = contributionLevel(hours, maxHours)
		}
		hoursLabel := ""
		if inRange {
			hoursLabel = formatHours(hours)
		}
		if inRange && day.Day() == 1 && day.Month() != lastMonth {
			months = append(months, ContributionMonth{
				Label:  day.Format("Jan"),
				Column: dayIndex/7 + 1,
			})
			lastMonth = day.Month()
		}
		days = append(days, ContributionDay{
			Date:       dateKey,
			Label:      day.Format("Jan 2, 2006"),
			Hours:      hours,
			HoursLabel: hoursLabel,
			Level:      level,
			InRange:    inRange,
		})
		dayIndex++
	}

	weeks := (dayIndex + 6) / 7
	if weeks < 1 {
		weeks = 1
	}

	return ContributionData{
		Days:       days,
		Months:     months,
		Weeks:      weeks,
		StartLabel: start.Format("Jan 2, 2006"),
		EndLabel:   end.Format("Jan 2, 2006"),
		MaxHours:   maxHours,
		TotalHours: totalHours,
	}
}

func enrichActivityView(view *ActivityView, activity storage.Activity) {
	view.TypeLabel = activityTypeLabel(activity.Type)
	view.TypeClass = activityTypeClass(activity.Type)
	view.IsHidden = isActivityHidden(activity)
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

func formatHours(hours float64) string {
	if hours <= 0 {
		return "No activity"
	}
	if hours < 10 {
		return fmt.Sprintf("%.1f h", hours)
	}
	return fmt.Sprintf("%.0f h", hours)
}

func contributionLevel(hours, max float64) int {
	if hours <= 0 || max <= 0 {
		return 0
	}
	ratio := hours / max
	switch {
	case ratio <= 0.25:
		return 1
	case ratio <= 0.5:
		return 2
	case ratio <= 0.75:
		return 3
	default:
		return 4
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
