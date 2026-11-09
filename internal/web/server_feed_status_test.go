package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestFeedShowsSavedActivitiesWithPendingOrFailedMaps(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "fixture"}); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{SessionSecret: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 3} {
		_, err = store.InsertActivity(ctx, storage.Activity{ID: id, UserID: 11, Type: "Ride", Name: fmt.Sprintf("saved-%d", id), StartTime: time.Unix(1700000000+id, 0), Distance: 12000, MovingTime: 1800}, []gps.Point{{Lat: 1, Lon: 1}, {Lat: 1.01, Lon: 1.01}})
		if err != nil {
			t.Fatal(err)
		}
		if id != 3 {
			if err = store.UpsertActivityStats(ctx, id, stats.StopStats{StopCount: 4}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, job := range []storage.Job{
		{UserID: 11, ActivityID: 1, Type: "enrich_activity", Status: "failed", ErrorCode: "map_unavailable"},
		{UserID: 11, ActivityID: 2, Type: "enrich_activity", Status: "queued"},
		{UserID: 11, ActivityID: 3, Type: "process_activity", Status: "failed"},
	} {
		if _, err = store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	cookieRec := httptest.NewRecorder()
	if err = s.setSession(cookieRec, httptest.NewRequest("GET", "/", nil), 11); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/mobile/activities", "/activities/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookieRec.Result().Cookies() {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		if strings.HasPrefix(path, "/api/") {
			s.MobileActivities(rec, req)
		} else {
			s.Activities(rec, req)
		}
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if strings.HasPrefix(path, "/api/") {
			var feed struct {
				Activities []struct {
					ID               int64  `json:"id"`
					ProcessingStatus string `json:"processing_status"`
					RouteStatus      string `json:"route_status"`
					StopCount        int    `json:"stop_count"`
				} `json:"activities"`
			}
			if err = json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
				t.Fatal(err)
			}
			if len(feed.Activities) != 3 {
				t.Fatalf("saved activities hidden: %s", rec.Body.String())
			}
			want := []string{"analysis_error", "maps_pending", "maps_error"}
			for i, a := range feed.Activities {
				if a.ID != int64(3-i) || a.ProcessingStatus != want[i] {
					t.Errorf("activity %d status=%q want=%q", a.ID, a.ProcessingStatus, want[i])
				}
				if a.RouteStatus != "available" {
					t.Errorf("GPS preview lost for %d", a.ID)
				}
				if a.ID != 3 && a.StopCount != 4 {
					t.Errorf("metrics lost for %d", a.ID)
				}
			}
		} else {
			for _, label := range []string{"Analysis unavailable", "Map details pending", "Map details unavailable"} {
				if !strings.Contains(rec.Body.String(), label) {
					t.Errorf("web badge missing %q", label)
				}
			}
		}
	}
}
