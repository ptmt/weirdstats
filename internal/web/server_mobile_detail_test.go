package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestMobileActivityDetailOwnedFullRouteAndStoredFacts(t *testing.T) {
	s, store := newAutomationTestServer(t)
	start := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	points := make([]gps.Point, 120)
	for i := range points {
		points[i] = gps.Point{Lat: 52 + float64(i)/10000, Lon: 13, Time: start.Add(time.Duration(i) * time.Second)}
	}
	_, err := store.InsertActivity(context.Background(), storage.Activity{ID: 42, UserID: 202, Name: "Test ride", Type: "Ride", StartTime: start, Distance: 1000, MovingTime: 300, AverageHeartRate: 140}, points)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertActivityDetectedFacts(context.Background(), 42, `[{"id":"segment","kind":"segment","title":"Segment","summary":"Stored segment evidence","color":"green","path":[{"lat":52,"lon":13},{"lat":52.001,"lon":13}]}]`, time.Now()); err != nil {
		t.Fatal(err)
	}
	token, _, _ := s.issueBearerToken(202)
	req := httptest.NewRequest("GET", "/api/mobile/activities/42", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.MobileActivityDetail(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Activity         mobileActivityView `json:"activity"`
		AverageHeartRate *float64           `json:"average_heart_rate"`
		Stops            []StopView         `json:"stops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Activity.ID != 42 || len(body.Activity.Route) != 120 || body.Activity.Facts[0].Summary != "Stored segment evidence" || len(body.Activity.Facts[0].Path) != 2 || body.AverageHeartRate == nil || *body.AverageHeartRate != 140 {
		t.Fatal("incomplete detail")
	}
	if body.Stops == nil {
		t.Fatal("stops must be an array")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("personal detail must not be cached")
	}
}

func TestMobileActivityDetailSecurityAndMissingGPS(t *testing.T) {
	s, store := newAutomationTestServer(t)
	_, err := store.InsertActivity(context.Background(), storage.Activity{ID: 42, UserID: 202, Type: "VirtualRide", Name: "Indoor", StartTime: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(context.Background(), storage.StravaToken{UserID: 303, AccessToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	owner, _, _ := s.issueBearerToken(202)
	other, _, _ := s.issueBearerToken(303)
	for _, tc := range []struct {
		path, token, method string
		status              int
	}{
		{"/api/mobile/activities/42", "", "GET", 401},
		{"/api/mobile/activities/42", other, "GET", 404},
		{"/api/mobile/activities/43", owner, "GET", 404},
		{"/api/mobile/activities/-42", owner, "GET", 404},
		{"/api/mobile/activities/42/extra", owner, "GET", 404},
		{"/api/mobile/activities/42", owner, "POST", 405},
		{"/api/mobile/activities/42", owner, "GET", 200},
	} {
		t.Run(tc.method+tc.path+tc.token[:min(3, len(tc.token))], func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			s.MobileActivityDetail(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("got %d want %d", rec.Code, tc.status)
			}
			if tc.status == 200 {
				var body map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				a := body["activity"].(map[string]any)
				if a["route_status"] != "no_gps" || a["facts_status"] != "pending" {
					t.Fatal("missing honest state")
				}
				if _, ok := body["average_heart_rate"]; ok {
					t.Fatal("invented HR")
				}
			}
		})
	}
}
