package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/storage"
)

func TestActivitiesShowsOneSelectableHistoryYear(t *testing.T) {
	server, store := newAutomationTestServer(t)
	currentYear := time.Now().Year()
	for i, year := range []int{currentYear, currentYear - 1, currentYear - 2, currentYear - 3} {
		userID := int64(202)
		if i == 3 {
			userID = 303
		}
		if _, err := store.InsertActivity(context.Background(), storage.Activity{
			ID: int64(42 + i), UserID: userID, Name: fmt.Sprintf("Ride in %d", year), Type: "Ride",
			StartTime: time.Date(year, time.January, 15, 12, 0, 0, 0, time.Local), MovingTime: 1800,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		path string
		year int
		day  bool
	}{
		{path: "/activities/", year: currentYear},
		{path: fmt.Sprintf("/activities/?year=%d", currentYear-2), year: currentYear - 2},
		{path: fmt.Sprintf("/activities/?day=%d-01-15", currentYear-1), year: currentYear - 1, day: true},
	} {
		t.Run(test.path, func(t *testing.T) {
			rec := authenticatedPage(t, server, test.path, server.Activities)
			body := rec.Body.String()
			if rec.Code != http.StatusOK || strings.Count(body, `class="card contrib-card"`) != 1 {
				t.Fatalf("expected exactly one calendar: status=%d", rec.Code)
			}
			if !strings.Contains(body, fmt.Sprintf(`<option value="%d" selected>`, test.year)) || !strings.Contains(body, fmt.Sprintf(`href="/activities/?day=%d-01-01"`, test.year)) {
				t.Fatalf("calendar did not show selected year %d", test.year)
			}
			if strings.Contains(body, fmt.Sprintf(`<option value="%d"`, currentYear-3)) {
				t.Fatal("another user's activity year appeared in the selector")
			}
			if test.day && (!strings.Contains(body, `aria-current="date"`) || !strings.Contains(body, fmt.Sprintf(`href="/activities/?year=%d"`, test.year))) {
				t.Fatal("day filter omitted the selected day or lost the year when cleared")
			}
		})
	}
	for _, invalid := range []string{"abc", "0", "-1", fmt.Sprint(currentYear + 1)} {
		rec := authenticatedPage(t, server, "/activities/?year="+invalid, server.Activities)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid year %q returned %d", invalid, rec.Code)
		}
	}
}
