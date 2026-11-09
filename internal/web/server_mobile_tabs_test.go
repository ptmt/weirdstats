package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestMobileActivitiesIncludesOwnedRoutesFactsAndYear(t *testing.T) {
	s, store := newAutomationTestServer(t)
	ctx := context.Background()
	year := time.Now().Year() - 1
	for i, user := range []int64{202, 303} {
		start := time.Date(year, time.January, 15, 12, 0, 0, 0, time.Local)
		id, err := store.InsertActivity(ctx, storage.Activity{ID: int64(900 + i), UserID: user, Name: fmt.Sprintf("Ride %d", user), Type: "Ride", StartTime: start, MovingTime: 1800}, []gps.Point{{Lat: 52.5, Lon: 13.4, Time: start}, {Lat: 52.6, Lon: 13.5, Time: start.Add(time.Minute)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertActivityDetectedFacts(ctx, id, `[{"id":"stops","kind":"stops","title":"Actual stops","summary":"Stored evidence","color":"green"}]`, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	token, _, err := s.issueBearerToken(202)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/mobile/activities?year=%d", year), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.MobileActivities(rec, req)
	var body struct {
		Activities []struct {
			ID    int64                 `json:"id"`
			Route []routePreviewPoint   `json:"route"`
			Facts []ActivityMapFactView `json:"facts"`
		}
		History *ContributionData `json:"history"`
		Years   []int             `json:"years"`
	}
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Activities) != 1 || body.Activities[0].ID != 900 || len(body.Activities[0].Route) != 2 || len(body.Activities[0].Facts) != 1 {
		t.Fatalf("missing owned route/facts: %+v", body)
	}
	if body.History == nil || body.History.Year != year || body.History.TotalEffort != 0.5 {
		t.Fatalf("missing full-year aggregation: %+v", body.History)
	}
}

func TestMobileHistoryAggregatesBeyondPageAndKeepsLeapDay(t *testing.T) {
	s, store := newAutomationTestServer(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		user := int64(202)
		year := 2024
		if i == 2 {
			user = 303
			year = 2023
		}
		_, err := store.InsertActivity(ctx, storage.Activity{ID: int64(1000 + i), UserID: user, Name: "History", Type: "VirtualRide", StartTime: time.Date(year, time.February, 29-i, 12, 0, 0, 0, time.Local), MovingTime: 3600}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	token, _, _ := s.issueBearerToken(202)
	req := httptest.NewRequest("GET", "/api/mobile/activities?year=2024&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.MobileActivities(rec, req)
	var body mobileActivitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Activities) != 1 || body.NextCursor == "" || body.History == nil || body.History.TotalEffort != 2 {
		t.Fatalf("partial aggregation: %+v", body)
	}
	found := false
	for _, day := range body.History.Days {
		if day.Date == "2024-02-29" {
			found = true
			if day.Effort != 1 {
				t.Fatal(day)
			}
		}
	}
	if !found {
		t.Fatal("leap day omitted")
	}
	for _, year := range body.Years {
		if year == 2023 {
			t.Fatal("foreign user's year leaked")
		}
	}
	if len(body.Activities[0].Route) != 0 || body.Activities[0].RouteStatus != "unavailable" || len(body.Activities[0].Facts) != 0 || body.Activities[0].FactsStatus != "pending" {
		t.Fatal("fabricated route or facts")
	}
}

func TestMobileActivitiesRejectsInvalidYearAndUnauthorized(t *testing.T) {
	s, _ := newAutomationTestServer(t)
	token, _, _ := s.issueBearerToken(202)
	for _, path := range []string{"/api/mobile/activities?year=no", "/api/mobile/activities?year=0", "/api/mobile/activities?year=9999"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.MobileActivities(rec, req)
		if rec.Code != 400 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.MobileActivities(rec, httptest.NewRequest("GET", "/api/mobile/activities?year=2025", nil))
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
}
