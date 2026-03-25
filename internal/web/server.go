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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/jobs"
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
	ID         string              `json:"id"`
	Kind       string              `json:"kind"`
	Title      string              `json:"title"`
	Summary    string              `json:"summary"`
	Color      string              `json:"color"`
	BadgeLabel string              `json:"badge_label,omitempty"`
	BadgeTone  string              `json:"badge_tone,omitempty"`
	Points     []ActivityFactPoint `json:"points,omitempty"`
	Path       []routePreviewPoint `json:"path,omitempty"`
}

type ActivityDataItem struct {
	Label  string
	Value  string
	Detail string
	Tone   string
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
	DataItems         []ActivityDataItem
	RoutePointsJSON   template.JS
	StopsJSON         template.JS
	DetectedFactsJSON template.JS
	SpeedSeriesJSON   template.JS
	SpeedThreshold    float64
	StopMinDuration   string
	HasRoutePoints    bool
	HasSpeedSeries    bool
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
	Facts []SettingsFact
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
	poster, err := template.New("poster").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/poster.html",
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
			"poster":   poster,
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
		Facts: buildSettingsFacts(defaultWeirdStatsFactSettings()),
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
