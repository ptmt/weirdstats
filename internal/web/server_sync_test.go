package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/storage"
)

func TestOAuthRequiresAndPersistsActivityScopes(t *testing.T) {
	for _, scope := range []string{"read", "activity:read", "activity:read_all"} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			store, err := storage.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.InitSchema(ctx); err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"access_token":"fake","refresh_token":"fake-refresh","expires_at":2000000000,"athlete":{"id":77}}`)
			}))
			defer upstream.Close()
			s, err := NewServer(store, &ingest.Ingestor{Store: store}, nil, nil, gps.StopOptions{}, StravaConfig{ClientID: "test", ClientSecret: "test", AuthBaseURL: upstream.URL, InitialSyncDays: 30})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.connectStravaUser(ctx, "fake-code", scope)
			rows, listErr := store.ListJobs(ctx, 10)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if scope == "read" {
				if err == nil || len(rows) != 0 {
					t.Fatalf("missing permissions accepted: %v %v", err, rows)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			token, err := store.GetStravaToken(ctx, 77)
			if err != nil {
				t.Fatal(err)
			}
			if token.Scopes != scope || token.ConnectionID == "" || len(rows) != 1 {
				t.Fatalf("token=%+v jobs=%d", token, len(rows))
			}
		})
	}
}

func TestSyncErrorsAreOwnedAndSanitized(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{11, 22} {
		if _, err = store.CreateJob(ctx, storage.Job{Type: "process_activity", UserID: id, ActivityID: id, Status: "failed", LastError: "sensitive provider URL", NextRunAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.syncView(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if view.Failed != 1 || len(view.Errors) != 1 || view.Errors[0].Message == "sensitive provider URL" {
		t.Fatalf("unsafe status: %+v", view)
	}
}

func TestLimitedReconnectPurgesBroaderDataAfterDisconnect(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 77, AccessToken: "old", Scopes: "activity:read_all"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpsertActivity(ctx, storage.Activity{ID: 42, UserID: 77, Name: "Private ride", IsPrivate: true, Type: "Ride", StartTime: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteStravaToken(ctx, 77); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"new","refresh_token":"refresh","expires_at":2000000000,"athlete":{"id":77}}`)
	}))
	defer upstream.Close()
	s, err := NewServer(store, &ingest.Ingestor{Store: store}, nil, nil, gps.StopOptions{}, StravaConfig{ClientID: "test", ClientSecret: "test", AuthBaseURL: upstream.URL, InitialSyncDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.connectStravaUser(ctx, "code", "activity:read"); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.HasImportedActivities(ctx, 77); err != nil || exists {
		t.Fatalf("private data retained: %v %v", exists, err)
	}
	rows, err := store.ListJobs(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("recovery jobs: %v %v", rows, err)
	}
}
