package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/ingest"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

func historyActivity(t *testing.T, store *storage.Store, userID, id int64) storage.Activity {
	t.Helper()
	activity := storage.Activity{ID: id, UserID: userID, Type: "VirtualRide", Name: "ROUVY workout", StartTime: time.Now(), Distance: 18100, MovingTime: 2697, Description: "ROUVY"}
	if _, err := store.InsertActivity(context.Background(), activity, nil); err != nil {
		t.Fatal(err)
	}
	return activity
}

func authenticatedPage(t *testing.T, server *Server, path string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	session := httptest.NewRecorder()
	if err := server.setSession(session, req, 202); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range session.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestNotificationsAndAutomationHistoryAcrossPages(t *testing.T) {
	server, store := newAutomationTestServer(t)
	ctx := context.Background()
	activity := historyActivity(t, store, 202, 20529500523)
	other := historyActivity(t, store, 303, 20529500524)
	if err := store.RecordAutomationEvents(ctx, 202, activity.ID, []storage.AutomationEvent{{Name: "Hide virtual rides", Action: "hide", Source: "automatic", Status: "applied", Detail: "Matched VirtualRide.", Triggered: true}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAutomationEvents(ctx, 303, other.ID, []storage.AutomationEvent{{Name: "Private other-user history", Action: "hide", Source: "automatic", Status: "applied", Triggered: true}}); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{202, 303} {
		if _, err := store.CreateJob(ctx, storage.Job{Type: "process_activity", UserID: userID, ActivityID: activity.ID, Status: "failed", LastError: "sensitive-provider-body"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/activities/", server.Activities}, {"/automation", server.Automation}, {"/activities/settings", server.Settings}, {"/admin/", server.Admin}, {fmt.Sprintf("/activity/%d", activity.ID), server.Activity},
	} {
		t.Run(page.path, func(t *testing.T) {
			rec := authenticatedPage(t, server, page.path, page.handler)
			body := rec.Body.String()
			if rec.Code != 200 {
				t.Fatalf("page failed: %d %s", rec.Code, body)
			}
			for _, text := range []string{`id="sync-status"`, "1 need attention", `src="/static/sync-status.js"`, `id="sync-new-activities" href="/activities/"`} {
				if !strings.Contains(body, text) {
					t.Fatalf("missing shared status %q", text)
				}
			}
			if strings.Contains(body, "sensitive-provider-body") || strings.Contains(body, "Private other-user history") {
				t.Fatal("history or notifications leaked another user's data")
			}
			if page.path == "/activities/" && !strings.Contains(body, `title="See recorded automation runs">1 automation</a>`) {
				t.Fatal("activity card omitted automation count")
			}
			if page.path == "/automation" || strings.HasPrefix(page.path, "/activity/") {
				if !strings.Contains(body, "Hide virtual rides") || !strings.Contains(body, "Applied") {
					t.Fatal("automation history omitted execution details")
				}
			}
		})
	}
	rec := authenticatedPage(t, server, fmt.Sprintf("/automation?activity=%d", other.ID), server.Automation)
	if rec.Code != 404 {
		t.Fatal("another user's activity history was accessible")
	}
}

func TestAutomationCanonicalURLAndPagination(t *testing.T) {
	server, store := newAutomationTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		server.Automation(rec, httptest.NewRequest(method, "/activities/automation?msg=hello", nil))
		if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/automation?msg=hello" {
			t.Fatalf("bad canonical redirect: %d %s", rec.Code, rec.Header().Get("Location"))
		}
	}
	activity := historyActivity(t, store, 202, 42)
	events := make([]storage.AutomationEvent, 51)
	for i := range events {
		events[i] = storage.AutomationEvent{Name: fmt.Sprintf("Rule %d", i), Action: "hide", Source: "automatic", Status: "applied", Triggered: true}
	}
	if err := store.RecordAutomationEvents(context.Background(), 202, activity.ID, events); err != nil {
		t.Fatal(err)
	}
	rec := authenticatedPage(t, server, "/automation?activity=42", server.Automation)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Older runs") || strings.Contains(rec.Body.String(), "<strong>Rule 0</strong>") {
		t.Fatal("history pagination did not limit the first page")
	}
	rec = authenticatedPage(t, server, "/automation?activity=42&before=2", server.Automation)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<strong>Rule 0</strong>") {
		t.Fatal("history pagination lost the older run")
	}
}

func TestApplyRecordsEveryMatchedRuleAndUpdateOutcome(t *testing.T) {
	for _, test := range []struct {
		name          string
		fail          bool
		description   bool
		webhook       bool
		alreadyHidden bool
		manual        bool
		disabled      bool
	}{
		{name: "hide all matching rules"}, {name: "failed update", fail: true}, {name: "description and hide", description: true}, {name: "update webhook preserves history", webhook: true},
		{name: "already muted", alreadyHidden: true},
		{name: "already muted despite description failure", alreadyHidden: true, description: true, fail: true},
		{name: "manual apply with automatic publishing off", manual: true},
		{name: "automatic publishing disabled before update", disabled: true, description: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, store := newAutomationTestServer(t)
			ctx := context.Background()
			activity := historyActivity(t, store, 202, 42)
			if err := store.SaveProcessingPreferences(ctx, 202, storage.ProcessingPreferences{AutoPublish: !test.disabled && !test.manual}); err != nil {
				t.Fatal(err)
			}
			preferences := make([]storage.UserFactPreference, 0, len(weirdStatsFactDefinitions))
			for _, fact := range weirdStatsFactDefinitions {
				preferences = append(preferences, storage.UserFactPreference{FactID: fact.ID, Enabled: test.description && fact.ID == weirdStatsFactStopSummary, PostToStrava: true})
			}
			if err := store.ReplaceUserFactPreferences(ctx, 202, preferences); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertActivityStats(ctx, activity.ID, stats.StopStats{StopCount: 2, StopTotalSeconds: 30}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Hide virtual rides", "Another matching hide rule"} {
				if _, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 202, Name: name, Enabled: true, Condition: `{"conditions":[{"metric":"activity_type","op":"eq","values":["VirtualRide"]}]}`}); err != nil {
					t.Fatal(err)
				}
			}
			updates := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					updates++
					if test.fail {
						w.WriteHeader(500)
						fmt.Fprint(w, `{"message":"sensitive-provider-body"}`)
						return
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if !test.alreadyHidden && r.FormValue("hide_from_home") != "true" {
						t.Error("matched rules did not mute the activity")
					}
					if test.description && (!strings.HasPrefix(r.FormValue("description"), "ROUVY") || !strings.Contains(r.FormValue("description"), "#weirdstats")) {
						t.Error("description was not published")
					}
					if test.webhook {
						if err := store.InvalidateActivity(ctx, 202, activity.ID, false); err != nil {
							t.Error(err)
						}
					}
				}
				fmt.Fprintf(w, `{"id":42,"type":"VirtualRide","name":"ROUVY workout","description":"ROUVY","start_date":"2026-10-10T12:13:00Z","distance":18100,"moving_time":2697,"hide_from_home":%t}`, test.alreadyHidden)
			}))
			defer upstream.Close()
			server.ingestor = &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL, AccessToken: "fake"}}
			err := server.Apply(storage.ContextWithAutomaticPublish(ctx, !test.manual), activity.ID)
			if (err != nil) != (test.fail || test.disabled) {
				t.Fatalf("unexpected apply error: %v", err)
			}
			expectedUpdates := 1
			if test.disabled || (test.alreadyHidden && !test.description) {
				expectedUpdates = 0
			}
			if updates != expectedUpdates {
				t.Fatalf("expected %d combined updates, got %d", expectedUpdates, updates)
			}
			if test.webhook {
				if _, err := store.UpsertActivity(ctx, activity, nil); err != nil {
					t.Fatal(err)
				}
			}
			events, err := store.ListAutomationEvents(ctx, 202, activity.ID, 0, 10)
			if err != nil || len(events) != 3 {
				t.Fatalf("history=%+v err=%v", events, err)
			}
			for _, event := range events {
				expectedSource := "automatic"
				if test.manual {
					expectedSource = "manual"
				}
				if event.Source != expectedSource || strings.Contains(event.Detail, "sensitive-provider-body") {
					t.Fatalf("unsafe or wrong source: %+v", event)
				}
				expectedStatus := "applied"
				if test.fail {
					expectedStatus = "failed"
				}
				if test.disabled {
					expectedStatus = "disabled"
				}
				hideStatus := expectedStatus
				if test.alreadyHidden {
					hideStatus = "already_hidden"
				}
				if event.Action == "hide" && (event.Status != hideStatus || event.Triggered == test.disabled) {
					t.Fatalf("incorrect hide result: %+v", event)
				}
				if event.Action == "description" && test.description && (event.Status != expectedStatus || event.Triggered == test.disabled) {
					t.Fatalf("incorrect description result: %+v", event)
				}
			}
			counts, err := store.CountTriggeredAutomations(ctx, 202, []int64{activity.ID})
			expectedCount := 2
			if test.description {
				expectedCount++
			}
			if test.disabled {
				expectedCount = 0
			}
			if err != nil || counts[activity.ID] != expectedCount {
				t.Fatalf("counts=%v err=%v", counts, err)
			}
		})
	}
}

func TestPrecomputeRecordsMismatchAndDisabledPublishing(t *testing.T) {
	server, store := newAutomationTestServer(t)
	ctx := context.Background()
	activity := historyActivity(t, store, 202, 42)
	if _, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 202, Name: "Hide workouts", Enabled: true, Condition: `{"conditions":[{"metric":"activity_type","op":"eq","values":["Workout"]}]}`}); err != nil {
		t.Fatal(err)
	}
	if err := server.PrecomputeActivityFacts(ctx, activity, stats.StopStats{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAutomationEvents(ctx, 202, activity.ID, 0, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("history=%+v err=%v", events, err)
	}
	for _, event := range events {
		if event.Triggered {
			t.Fatal("local analysis counted as a published automation")
		}
		if event.Action == "hide" && (event.Status != "no_match" || !strings.Contains(event.Detail, "VirtualRide")) {
			t.Fatalf("missing mismatch explanation: %+v", event)
		}
		if event.Action == "publish" && event.Status != "disabled" {
			t.Fatalf("missing disabled explanation: %+v", event)
		}
	}
}
