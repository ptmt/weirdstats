package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"weirdstats/internal/storage"
)

type mobileAthleteView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type mobileSessionExchangeRequest struct {
	Grant string `json:"grant"`
}

type mobileSessionExchangeResponse struct {
	AccessToken string            `json:"access_token"`
	TokenType   string            `json:"token_type"`
	ExpiresAt   int64             `json:"expires_at"`
	Athlete     mobileAthleteView `json:"athlete"`
}

type mobileMeResponse struct {
	UserID     int64             `json:"user_id"`
	Connected  bool              `json:"connected"`
	Athlete    mobileAthleteView `json:"athlete"`
	BaseURL    string            `json:"base_url,omitempty"`
	Activities string            `json:"activities_url"`
}

type mobileActivitiesResponse struct {
	Activities []mobileActivityView `json:"activities"`
}

type mobileActivityView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	TypeLabel         string `json:"type_label"`
	StartTime         string `json:"start_time"`
	Distance          string `json:"distance"`
	Duration          string `json:"duration"`
	StopCount         int    `json:"stop_count"`
	LightStops        int    `json:"light_stops"`
	RoadCrossings     int    `json:"road_crossings"`
	DetectedFactCount int    `json:"detected_fact_count"`
	PhotoURL          string `json:"photo_url,omitempty"`
}

func (s *Server) requireAPIUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, ok := s.currentUserID(r.Context(), r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="weirdstats"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return 0, false
	}
	return userID, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func validMobileAppRedirect(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if parsed.Scheme == "" {
		return "", false
	}
	if strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	return parsed.String(), true
}

func (s *Server) mobileAppRedirectURL(r *http.Request) (string, bool) {
	if configured, ok := validMobileAppRedirect(s.strava.MobileAppRedirectURL); ok {
		return configured, true
	}
	return validMobileAppRedirect(r.URL.Query().Get("app_redirect"))
}

func (s *Server) mobileAuthorizeRedirectURL(r *http.Request) string {
	if configured := strings.TrimSpace(s.strava.MobileRedirectURL); configured != "" {
		return configured
	}
	scheme := "http"
	if requestIsSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/connect/strava/mobile/callback"
}

func (s *Server) ConnectStravaMobile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/connect/strava/mobile" {
		http.NotFound(w, r)
		return
	}
	if s.strava.ClientID == "" || s.strava.ClientSecret == "" {
		http.Error(w, "strava client not configured", http.StatusInternalServerError)
		return
	}
	appRedirect, ok := s.mobileAppRedirectURL(r)
	if !ok {
		http.Error(w, "mobile app redirect not configured", http.StatusInternalServerError)
		return
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

	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "failed to initialize oauth state", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, oauthStateCookieName, state, 10*60)
	setCookie(w, r, oauthAppCookieName, base64.RawURLEncoding.EncodeToString([]byte(appRedirect)), 10*60)

	params := url.Values{}
	params.Set("client_id", s.strava.ClientID)
	params.Set("redirect_uri", s.mobileAuthorizeRedirectURL(r))
	params.Set("response_type", "code")
	params.Set("state", state)
	params.Set("approval_prompt", "auto")
	params.Set("scope", "read,activity:read_all,activity:write")

	http.Redirect(w, r, endpoint+"?"+params.Encode(), http.StatusFound)
}

func (s *Server) StravaMobileCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/connect/strava/mobile/callback" {
		http.NotFound(w, r)
		return
	}

	expectedState := readCookieValue(r, oauthStateCookieName)
	appEncoded := readCookieValue(r, oauthAppCookieName)
	clearCookie(w, r, oauthStateCookieName)
	clearCookie(w, r, oauthAppCookieName)

	appRedirect := ""
	if decoded, err := base64.RawURLEncoding.DecodeString(appEncoded); err == nil {
		appRedirect = string(decoded)
	}
	if _, ok := validMobileAppRedirect(appRedirect); !ok {
		http.Error(w, "mobile app redirect not configured", http.StatusInternalServerError)
		return
	}

	if expectedState == "" || r.URL.Query().Get("state") != expectedState {
		http.Redirect(w, r, appendQueryValue(appRedirect, "error", "invalid_oauth_state"), http.StatusFound)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Redirect(w, r, appendQueryValue(appRedirect, "error", "strava_authorization_failed"), http.StatusFound)
		return
	}

	userID, err := s.connectStravaUser(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Redirect(w, r, appendQueryValue(appRedirect, "error", compactForLog(err.Error(), 64)), http.StatusFound)
		return
	}
	grant, expiresAt, err := s.issueMobileGrant(userID)
	if err != nil {
		http.Redirect(w, r, appendQueryValue(appRedirect, "error", "session_creation_failed"), http.StatusFound)
		return
	}

	redirectURL := appendQueryValue(appRedirect, "grant", grant)
	redirectURL = appendQueryValue(redirectURL, "expires_at", strconv.FormatInt(expiresAt.Unix(), 10))
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) MobileSessionExchange(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/mobile/session/exchange" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req mobileSessionExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	payload, ok := s.parseSignedAuthToken(strings.TrimSpace(req.Grant), mobileGrantKind)
	if !ok {
		http.Error(w, "invalid grant", http.StatusUnauthorized)
		return
	}

	token, err := s.store.GetStravaToken(r.Context(), payload.UserID)
	if err != nil {
		http.Error(w, "unknown user", http.StatusUnauthorized)
		return
	}
	accessToken, expiresAt, err := s.issueBearerToken(payload.UserID)
	if err != nil {
		http.Error(w, "failed to issue session", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, mobileSessionExchangeResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresAt:   expiresAt.Unix(),
		Athlete:     buildMobileAthleteView(token),
	})
}

func (s *Server) MobileMe(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/mobile/me" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := s.requireAPIUserID(w, r)
	if !ok {
		return
	}
	token, err := s.store.GetStravaToken(r.Context(), userID)
	if err != nil {
		http.Error(w, "unknown user", http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, mobileMeResponse{
		UserID:     userID,
		Connected:  true,
		Athlete:    buildMobileAthleteView(token),
		BaseURL:    strings.TrimRight(r.Host, "/"),
		Activities: "/api/mobile/activities",
	})
}

func (s *Server) MobileActivities(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/mobile/activities" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := s.requireAPIUserID(w, r)
	if !ok {
		return
	}

	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}

	activities, err := s.store.ListActivitiesWithStats(r.Context(), userID, limit)
	if err != nil {
		http.Error(w, "failed to load activities", http.StatusInternalServerError)
		return
	}

	items := make([]mobileActivityView, 0, len(activities))
	for _, activity := range activities {
		items = append(items, buildMobileActivityView(activity))
	}
	writeJSON(w, http.StatusOK, mobileActivitiesResponse{Activities: items})
}

func buildMobileAthleteView(token storage.StravaToken) mobileAthleteView {
	return mobileAthleteView{
		ID:   token.AthleteID,
		Name: strings.TrimSpace(token.AthleteName),
	}
}

func buildMobileActivityView(activity storage.ActivityWithStats) mobileActivityView {
	_, detectedFactCount := splitStoredActivityDescription(activity.Description)
	view := ActivityView{}
	enrichActivityView(&view, activity.Activity)
	return mobileActivityView{
		ID:                activity.ID,
		Name:              activity.Name,
		Type:              activity.Type,
		TypeLabel:         view.TypeLabel,
		StartTime:         activity.StartTime.Format("2006-01-02T15:04:05Z07:00"),
		Distance:          formatDistance(activity.Distance),
		Duration:          formatDuration(activity.MovingTime),
		StopCount:         activity.StopCount,
		LightStops:        activity.TrafficLightStopCount,
		RoadCrossings:     activity.RoadCrossingCount,
		DetectedFactCount: detectedFactCount,
		PhotoURL:          activity.PhotoURL,
	}
}
