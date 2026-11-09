package web

import (
	"context"
	"net/http/httptest"
	"testing"
	"weirdstats/internal/storage"
)

func TestLatestAcceptsOnlyAppBearerNotStravaOrGrant(t *testing.T) {
	s, store := newAutomationTestServer(t)
	bearer, _, err := s.issueBearerToken(202)
	if err != nil {
		t.Fatal(err)
	}
	grant, _, err := s.issueMobileGrant(202)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		want  int
	}{{"token", 401}, {grant, 401}, {bearer, 202}} {
		req := httptest.NewRequest("POST", "/api/sync/latest?user_id=11", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rec := httptest.NewRecorder()
		s.SyncLatest(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("status=%d wanted=%d", rec.Code, tc.want)
		}
	}
	rows, err := store.ListJobs(context.Background(), 10)
	if err != nil || len(rows) != 1 || rows[0].UserID != 202 {
		t.Fatalf("bad bearer ownership: %+v %v", rows, err)
	}
}

func TestRequestSyncOwnerCSRFAndSingleFlight(t *testing.T) {
	s, store := newAutomationTestServer(t)
	if err := store.UpsertStravaToken(context.Background(), storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, origin string
		owner                int64
		want                 int
	}{
		{"anonymous", "POST", "https://example.com", 0, 401},
		{"cross origin", "POST", "https://evil.test", 11, 403},
		{"read only", "GET", "https://example.com", 11, 405},
		{"owner", "POST", "https://example.com", 11, 202},
		{"duplicate", "POST", "https://example.com", 11, 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "https://example.com/api/sync/latest?user_id=22", nil)
			req.Header.Set("Origin", tc.origin)
			if tc.owner != 0 {
				rec := httptest.NewRecorder()
				if err := s.setSession(rec, req, tc.owner); err != nil {
					t.Fatal(err)
				}
				for _, c := range rec.Result().Cookies() {
					req.AddCookie(c)
				}
			}
			rec := httptest.NewRecorder()
			s.SyncLatest(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	rows, err := store.ListJobs(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UserID != 11 || rows[0].Type != "sync_latest" {
		t.Fatalf("wrong jobs: %+v", rows)
	}
	// Optional map failures were not retried as a side effect.
	_, err = store.CreateJob(context.Background(), storage.Job{Type: "enrich_activity", UserID: 11, ActivityID: 42, Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RequestLatestSync(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.ListJobs(context.Background(), 10)
	if len(rows) != 2 {
		t.Fatalf("duplicates: %+v", rows)
	}
	for _, j := range rows {
		if j.Type == "enrich_activity" && j.Status != "failed" {
			t.Fatal("retried optional maps")
		}
	}
}
