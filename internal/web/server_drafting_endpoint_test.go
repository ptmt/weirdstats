package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestActivityDraftingRecalculatesSavedRide(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	points := draftingTestPoints(func(second int) (float64, float64, float64) {
		if second >= 67 && second < 81 {
			return 195, 10, 0
		}
		return 260, 10, 0
	})
	meter := true
	if _, err := store.InsertActivity(ctx, storage.Activity{
		ID: 42, UserID: 11, Type: "Ride", Name: "TTT", StartTime: points[0].Time,
		DeviceWatts: &meter, PowerSourceChecked: true,
	}, points); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertActivity(ctx, storage.Activity{
		ID: 43, UserID: 22, Type: "Ride", Name: "Another rider", StartTime: points[0].Time,
		DeviceWatts: &meter, PowerSourceChecked: true,
	}, points); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatal(err)
	}
	sessionRec := httptest.NewRecorder()
	if err := s.setSession(sessionRec, httptest.NewRequest(http.MethodGet, "/activity/42", nil), 11); err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range sessionRec.Result().Cookies() {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		s.Activity(rec, req)
		return rec
	}
	short := request("/activity/42/drafting?window=10&drop=12")
	long := request("/activity/42/drafting?window=30&drop=12")
	if short.Code != http.StatusOK || long.Code != http.StatusOK {
		t.Fatalf("analysis responses: short=%d long=%d", short.Code, long.Code)
	}
	var shortView, longView DraftingView
	if err := json.Unmarshal(short.Body.Bytes(), &shortView); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(long.Body.Bytes(), &longView); err != nil {
		t.Fatal(err)
	}
	if shortView.Count == 0 || longView.Count != 0 || shortView.WindowSeconds != 10 {
		t.Fatalf("options did not recalculate: short=%+v long=%+v", shortView, longView)
	}
	if invalid := request("/activity/42/drafting?window=2&drop=12"); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid window accepted: %d", invalid.Code)
	}
	if other := request("/activity/43/drafting?window=10&drop=12"); other.Code != http.StatusNotFound {
		t.Fatalf("other activity visible: %d", other.Code)
	}
	page := request("/activity/42")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Possible shifts along the ride") ||
		!strings.Contains(page.Body.String(), "const initialView = {") ||
		!strings.Contains(page.Body.String(), "const rideSeries = [{") ||
		!strings.Contains(page.Body.String(), "after start") {
		t.Fatalf("ride page missing timeline data: status=%d", page.Code)
	}
}
