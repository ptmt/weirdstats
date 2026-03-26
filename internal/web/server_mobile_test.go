package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestConnectStravaMobile_StartsOAuthFlow(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{
		ClientID:             "client-123",
		ClientSecret:         "secret-123",
		AuthBaseURL:          "https://strava.example",
		MobileAppRedirectURL: "weirdstats://auth/strava",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://weirdstats.example/connect/strava/mobile", nil)
	rec := httptest.NewRecorder()

	server.ConnectStravaMobile(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "https://strava.example/oauth/authorize?") {
		t.Fatalf("unexpected redirect: %q", location)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("redirect_uri"); got != "https://weirdstats.example/connect/strava/mobile/callback" {
		t.Fatalf("unexpected redirect_uri: %q", got)
	}
	if got := query.Get("scope"); got != "read,activity:read_all,activity:write" {
		t.Fatalf("unexpected scope: %q", got)
	}
	if query.Get("state") == "" {
		t.Fatalf("expected oauth state in redirect")
	}

	var hasStateCookie bool
	var hasAppCookie bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == oauthStateCookieName && cookie.Value != "" {
			hasStateCookie = true
		}
		if cookie.Name == oauthAppCookieName {
			decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
			if err != nil {
				t.Fatalf("decode app cookie: %v", err)
			}
			if string(decoded) != "weirdstats://auth/strava" {
				t.Fatalf("unexpected app redirect cookie: %q", decoded)
			}
			hasAppCookie = true
		}
	}
	if !hasStateCookie || !hasAppCookie {
		t.Fatalf("expected oauth cookies to be set")
	}
}

func TestStravaMobileCallback_RedirectsBackToAppWithGrant(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	stravaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("code"); got != "mobile-code" {
			t.Fatalf("unexpected code: %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":  "strava-access",
			"refresh_token": "strava-refresh",
			"expires_at":    time.Now().Add(time.Hour).Unix(),
			"athlete": map[string]any{
				"id":        int64(77),
				"firstname": "Mina",
				"lastname":  "Runner",
			},
		})
	}))
	defer stravaServer.Close()

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{
		ClientID:      "client-123",
		ClientSecret:  "secret-123",
		AuthBaseURL:   stravaServer.URL,
		RedirectURL:   "https://weirdstats.example/connect/strava/callback",
		SessionSecret: "mobile-test-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/connect/strava/mobile/callback?state=state-123&code=mobile-code", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: "state-123"})
	req.AddCookie(&http.Cookie{Name: oauthAppCookieName, Value: base64.RawURLEncoding.EncodeToString([]byte("weirdstats://auth/strava"))})
	rec := httptest.NewRecorder()

	server.StravaMobileCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "weirdstats://auth/strava?") {
		t.Fatalf("unexpected redirect: %q", location)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	grant := parsed.Query().Get("grant")
	if grant == "" {
		t.Fatalf("expected mobile grant in redirect")
	}
	if _, ok := server.parseSignedAuthToken(grant, mobileGrantKind); !ok {
		t.Fatalf("expected valid mobile grant")
	}

	token, err := store.GetStravaToken(ctx, 77)
	if err != nil {
		t.Fatalf("stored strava token: %v", err)
	}
	if token.AthleteName != "Mina Runner" {
		t.Fatalf("unexpected athlete name: %q", token.AthleteName)
	}
}

func TestMobileSessionExchange_IssuesBearerTokenAndMe(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:      88,
		AccessToken: "strava-access",
		AthleteID:   88,
		AthleteName: "Ari Rider",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{
		SessionSecret: "mobile-test-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	grant, _, err := server.issueMobileGrant(88)
	if err != nil {
		t.Fatalf("issue grant: %v", err)
	}

	body, err := json.Marshal(mobileSessionExchangeRequest{Grant: grant})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/session/exchange", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.MobileSessionExchange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var session mobileSessionExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if session.TokenType != "Bearer" || session.AccessToken == "" {
		t.Fatalf("expected bearer token, got %+v", session)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/mobile/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+session.AccessToken)
	meRec := httptest.NewRecorder()

	server.MobileMe(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from me, got %d: %s", meRec.Code, meRec.Body.String())
	}
	var me mobileMeResponse
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.UserID != 88 || me.Athlete.Name != "Ari Rider" {
		t.Fatalf("unexpected me payload: %+v", me)
	}
}

func TestMobileActivities_ReturnsRecentActivityList(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:      99,
		AccessToken: "strava-access",
		AthleteID:   99,
		AthleteName: "Nia Example",
	}); err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	start := time.Date(2026, time.March, 26, 7, 30, 0, 0, time.UTC)
	activityID, err := store.InsertActivity(ctx, storage.Activity{
		UserID:      99,
		Type:        "Ride",
		Name:        "Morning Loop",
		StartTime:   start,
		Description: "Coffee.\n\n2 stops (42s total) · 1 at lights #weirdstats",
		Distance:    32450,
		MovingTime:  4012,
		PhotoURL:    "https://images.example/photo.jpg",
	}, []gps.Point{{Lat: 52.52, Lon: 13.405, Time: start, Speed: 7}})
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := store.UpsertActivityStats(ctx, activityID, stats.StopStats{
		StopCount:             2,
		StopTotalSeconds:      42,
		TrafficLightStopCount: 1,
		RoadCrossingCount:     3,
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}

	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{
		SessionSecret: "mobile-test-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	bearer, _, err := server.issueBearerToken(99)
	if err != nil {
		t.Fatalf("issue bearer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/activities?limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()

	server.MobileActivities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload mobileActivitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode activities: %v", err)
	}
	if len(payload.Activities) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(payload.Activities))
	}
	item := payload.Activities[0]
	if item.Name != "Morning Loop" || item.DetectedFactCount != 2 || item.RoadCrossings != 3 {
		t.Fatalf("unexpected activity payload: %+v", item)
	}
}
