package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/ingest"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Server struct {
	store     *storage.Store
	ingestor  *ingest.Ingestor
	templates map[string]*template.Template
	strava    StravaConfig
}

type ActivityView struct {
	ID          int64
	Name        string
	Type        string
	StartTime   string
	Description string
	HasStats    bool
	StopCount   int
	StopTotal   string
	LightStops  int
}

type StravaInfo struct {
	Connected   bool
	AthleteID   int64
	AthleteName string
}

type PageData struct {
	Title   string
	Page    string
	Message string
	Strava  StravaInfo
}

type LandingPageData struct {
	PageData
}

type ProfilePageData struct {
	PageData
	Activities []ActivityView
}

type SettingsRule struct {
	ID          int64
	Name        string
	Description string
	Enabled     bool
}

type SettingsPageData struct {
	PageData
	Rules []SettingsRule
}

type AdminPageData struct {
	PageData
	QueueCount int
}

type StravaConfig struct {
	ClientID     string
	ClientSecret string
	AuthBaseURL  string
	RedirectURL  string
}

func NewServer(store *storage.Store, ingestor *ingest.Ingestor, stravaConfig StravaConfig) (*Server, error) {
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
		"templates/landing.html",
	)
	if err != nil {
		return nil, err
	}
	profile, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/profile.html",
	)
	if err != nil {
		return nil, err
	}
	settings, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/settings.html",
	)
	if err != nil {
		return nil, err
	}
	admin, err := template.New("base").Funcs(funcs).ParseFS(
		templatesFS,
		"templates/base.html",
		"templates/admin.html",
	)
	if err != nil {
		return nil, err
	}
	return &Server{
		store:    store,
		ingestor: ingestor,
		strava:   stravaConfig,
		templates: map[string]*template.Template{
			"landing":  landing,
			"profile":  profile,
			"settings": settings,
			"admin":    admin,
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
			Title:   "weirdstats",
			Page:    "home",
			Message: r.URL.Query().Get("msg"),
			Strava:  s.getStravaInfo(r.Context()),
		},
	}
	if err := s.templates["landing"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
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
			HasStats:    activity.HasStats,
			StopCount:   activity.StopCount,
			StopTotal:   formatDuration(activity.StopTotalSeconds),
			LightStops:  activity.TrafficLightStopCount,
		}
		views = append(views, view)
	}
	data := ProfilePageData{
		PageData: PageData{
			Title:   "Profile",
			Page:    "profile",
			Message: r.URL.Query().Get("msg"),
			Strava:  s.getStravaInfo(r.Context()),
		},
		Activities: views,
	}
	if err := s.templates["profile"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
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

	rules, err := s.store.ListHideRules(r.Context(), 1)
	if err != nil {
		http.Error(w, "failed to load rules", http.StatusInternalServerError)
		return
	}
	var viewRules []SettingsRule
	for _, rule := range rules {
		viewRules = append(viewRules, SettingsRule{
			ID:          rule.ID,
			Name:        rule.Name,
			Description: rule.Condition,
			Enabled:     rule.Enabled,
		})
	}

	data := SettingsPageData{
		PageData: PageData{
			Title:   "Settings",
			Page:    "settings",
			Message: r.URL.Query().Get("msg"),
			Strava:  s.getStravaInfo(r.Context()),
		},
		Rules: viewRules,
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
			Title:   "Admin",
			Page:    "admin",
			Message: r.URL.Query().Get("msg"),
			Strava:  s.getStravaInfo(r.Context()),
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
