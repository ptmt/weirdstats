package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Server struct {
	store     *storage.Store
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

type ProfilePageData struct {
	Title           string
	Message         string
	StravaConnected bool
	Activities      []ActivityView
}

type SettingsRule struct {
	ID          int64
	Name        string
	Description string
	Enabled     bool
}

type SettingsPageData struct {
	Title   string
	Message string
	Rules   []SettingsRule
}

type StravaConfig struct {
	ClientID     string
	ClientSecret string
	AuthBaseURL  string
	RedirectURL  string
}

func NewServer(store *storage.Store, stravaConfig StravaConfig) (*Server, error) {
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
	return &Server{
		store:  store,
		strava: stravaConfig,
		templates: map[string]*template.Template{
			"landing":  landing,
			"profile":  profile,
			"settings": settings,
		},
	}, nil
}

func (s *Server) Landing(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := s.templates["landing"].ExecuteTemplate(w, "base", map[string]string{
		"Title":   "weirdstats",
		"Message": r.URL.Query().Get("msg"),
	}); err != nil {
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
	_, tokenErr := s.store.GetStravaToken(r.Context(), 1)
	stravaConnected := tokenErr == nil
	data := ProfilePageData{
		Title:           "Profile",
		Message:         r.URL.Query().Get("msg"),
		StravaConnected: stravaConnected,
		Activities:      views,
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
		Title:   "Settings",
		Message: r.URL.Query().Get("msg"),
		Rules:   viewRules,
	}
	if err := s.templates["settings"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
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
	params.Set("approval_prompt", "auto")
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
		http.Redirect(w, r, "/profile/settings?msg=strava+authorization+failed", http.StatusFound)
		return
	}
	if err := s.store.UpsertStravaToken(r.Context(), storage.StravaToken{
		UserID:       1,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Unix(token.ExpiresAt, 0),
	}); err != nil {
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
