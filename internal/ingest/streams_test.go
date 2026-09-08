package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

func TestMissingStreamsPreserveMetadataAndKnownAbsence(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			ctx := ContextWithUserID(context.Background(), 11)
			store, err := storage.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.InitSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "fake"}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if strings.HasSuffix(r.URL.Path, "/streams") {
					w.WriteHeader(404)
					fmt.Fprint(w, `{}`)
					return
				}
				fmt.Fprintf(w, `{"id":42,"name":"test","type":"Workout","start_date":"2026-09-01T12:00:00Z","manual":%t}`, manual)
			}))
			defer upstream.Close()
			i := Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL}}
			err = i.EnsureActivity(ctx, 42)
			if manual && err != nil {
				t.Fatal(err)
			}
			if !manual && err == nil {
				t.Fatal("unexpected stream failure was hidden")
			}
			if exists, err := store.HasActivity(ctx, 42); err != nil || !exists {
				t.Fatalf("metadata missing: %v", err)
			}
			if manual {
				if err = i.EnsureActivity(ctx, 42); err != nil {
					t.Fatal(err)
				}
				if calls != 2 {
					t.Fatalf("unavailable streams refetched: %d calls", calls)
				}
				activities, err := store.ListActivitiesWithStats(ctx, 11, 10)
				if err != nil || len(activities) != 1 || activities[0].GPSStatus == "" {
					t.Fatalf("missing GPS outcome: %v %v", activities, err)
				}
			}
		})
	}
}
