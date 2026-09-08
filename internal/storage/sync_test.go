package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
)

func syncTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeletionFencesFetchAndTokenRefresh(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	token := StravaToken{UserID: 11, AccessToken: "old", RefreshToken: "old-refresh", ConnectionID: "first"}
	if err := s.UpsertStravaToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	fetchCtx, err := s.ActivityFetchContext(ctx, 11, 42, "first")
	if err != nil {
		t.Fatal(err)
	}
	a := Activity{ID: 42, UserID: 11, Name: "test", Type: "Ride", StartTime: time.Now()}
	if _, err = s.UpsertActivity(fetchCtx, a, []gps.Point{{Lat: 1, Lon: 2, Time: a.StartTime}}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceActivityStops(ctx, 42, []ActivityStop{{Lat: 1, Lon: 2}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueActivity(ctx, 42, 11); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUserData(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertActivity(fetchCtx, a, nil); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("stale fetch: %v", err)
	}
	if err = s.RefreshStravaToken(ctx, token); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("stale refresh: %v", err)
	}
	if err = s.UpsertActivityStats(ctx, 42, stats.StopStats{}); err == nil {
		t.Fatal("orphan stats were accepted")
	}
	for _, table := range []string{"activities", "activity_points", "activity_stops", "activity_stats", "jobs", "strava_tokens"} {
		var n int
		if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s retained %d rows", table, n)
		}
	}
	if err = s.UpsertStravaToken(ctx, StravaToken{UserID: 11, AccessToken: "new", ConnectionID: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertActivity(fetchCtx, a, nil); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("reconnect admitted old fetch: %v", err)
	}
}

func TestActivityInvalidationFencesEarlierFetch(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	if err := s.UpsertStravaToken(ctx, StravaToken{UserID: 11, AccessToken: "fake", ConnectionID: "first"}); err != nil {
		t.Fatal(err)
	}
	old, err := s.ActivityFetchContext(ctx, 11, 42, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InvalidateActivity(ctx, 11, 42, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActivityFetchContext(ctx, 11, 42, "first"); !errors.Is(err, ErrConnectionChanged) {
		t.Fatal(err)
	}
	if err = s.InvalidateActivity(ctx, 11, 42, false); err != nil {
		t.Fatal(err)
	}
	a := Activity{ID: 42, UserID: 11, Name: "test", Type: "Ride", StartTime: time.Now()}
	if _, err = s.UpsertActivity(old, a, nil); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("old fetch accepted: %v", err)
	}
	if _, err = s.GuardActivityContext(old, 42); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("old context was replaced with a fresh authorization: %v", err)
	}
	fresh, err := s.ActivityFetchContext(ctx, 11, 42, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertActivity(fresh, a, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLargeBacklogDeduplicationAndFairness(t *testing.T) {
	for _, size := range []int{1000, 10000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx := context.Background()
			s := syncTestStore(t)
			start := time.Now()
			for id := 1; id <= size; id++ {
				if err := s.EnqueueActivity(ctx, int64(id), 11); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.EnqueueActivity(ctx, 1, 11); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != size {
				t.Fatalf("duplicate work: %d", count)
			}
			if err := s.EnqueueActivity(ctx, 20001, 22); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListUserJobs(ctx, 22, true, 20)
			if err != nil || len(rows) != 1 {
				t.Fatalf("other user's status hidden: %v %v", rows, err)
			}
			for _, want := range []int64{11, 22} {
				job, err := s.ClaimJob(ctx, time.Now().Add(time.Second), 10*time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if job.UserID != want {
					t.Fatalf("got user %d; want %d", job.UserID, want)
				}
				if err = s.MarkJobCompleted(ctx, job.ID, job.Cursor); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("queued %d jobs and checked deduplication/fairness in %s", size, time.Since(start))
		})
	}
}

func TestSyncPageIsAtomicWhenParentRemoved(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	parent := Job{ID: 1, UserID: 11, Type: "sync_activities_since"}
	err := s.EnqueueSyncPage(ctx, parent, []Job{{Type: "process_activity", UserID: 11, ActivityID: 42}}, "{}", "completed", time.Now())
	if err == nil {
		t.Fatal("removed parent accepted children")
	}
	rows, err := s.ListJobs(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial children: %v %v", rows, err)
	}
}

func TestRemovedJobCannotEnqueueLaterStages(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	id, err := s.CreateJob(ctx, Job{Type: "process_activity", UserID: 11, ActivityID: 42})
	if err != nil {
		t.Fatal(err)
	}
	ctx = ContextWithJob(ctx, id)
	if err = s.DeleteUserData(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateJob(ctx, Job{Type: "enrich_activity", UserID: 11, ActivityID: 42}); err == nil {
		t.Fatal("deleted user got a new job")
	}
}

func TestRoutePreviewsAreStoredAndDeletedWithActivity(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	points := make([]gps.Point, 3600)
	for i := range points {
		points[i] = gps.Point{Lat: 52 + float64(i)/100000, Lon: 13, Time: time.Unix(int64(i), 0)}
	}
	_, err := s.UpsertActivity(ctx, Activity{ID: 42, UserID: 11, Name: "test", Type: "Ride", StartTime: time.Now()}, points)
	if err != nil {
		t.Fatal(err)
	}
	// The feed must use the cached preview, even if raw points are unavailable.
	if _, err = s.db.ExecContext(ctx, "DELETE FROM activity_points"); err != nil {
		t.Fatal(err)
	}
	previews, err := s.ListActivityRoutePreviewPoints(ctx, []int64{42}, 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(previews[42]) < 2 || len(previews[42]) > 49 {
		t.Fatal(len(previews[42]))
	}
	if err = s.DeleteUserData(ctx, 11); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity_route_previews").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("route preview retained after deletion")
	}
}

func TestScopeDowngradeTransactionRollsBackOnEnqueueFailure(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	old := StravaToken{UserID: 11, AccessToken: "old", Scopes: "activity:read_all", ConnectionID: "old"}
	if err := s.UpsertStravaToken(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertActivity(ctx, Activity{ID: 42, UserID: 11, Name: "Private ride", IsPrivate: true, Type: "Ride", StartTime: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	limited := StravaToken{UserID: 11, AccessToken: "new", Scopes: "activity:read", ConnectionID: "new"}
	if err := s.CompleteStravaConnection(ctx, limited, true, &Job{}); err == nil {
		t.Fatal("invalid enqueue was accepted")
	}
	token, err := s.GetStravaToken(ctx, 11)
	if err != nil || token.ConnectionID != "old" {
		t.Fatalf("partial token change: %+v %v", token, err)
	}
	if _, err = s.GetActivity(ctx, 42); err != nil {
		t.Fatalf("partial purge: %v", err)
	}
	if err = s.CompleteStravaConnection(ctx, limited, true, &Job{Type: "sync_activities_since", UserID: 11}); err != nil {
		t.Fatal(err)
	}
	if exists, err := s.HasImportedActivities(ctx, 11); err != nil || exists {
		t.Fatalf("old data retained: %v %v", exists, err)
	}
	rows, err := s.ListJobs(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("recovery not queued: %v %v", rows, err)
	}
}

func TestPaginationHandlesTiedTimesAndUserIsolation(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	start := time.Now().Truncate(time.Second)
	for id := int64(1); id <= 5; id++ {
		user := int64(11)
		if id == 5 {
			user = 22
		}
		if _, err := s.UpsertActivity(ctx, Activity{ID: id, UserID: user, Name: "Ride", StartTime: start, Type: "Ride"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	var cursor ActivityCursor
	for {
		rows, err := s.ListActivitiesPage(ctx, 11, 2, time.Time{}, time.Time{}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		last := rows[len(rows)-1]
		cursor = ActivityCursor{ID: last.ID, StartUnix: last.StartTime.Unix()}
		if len(ids) > 4 {
			t.Fatal("cursor did not advance")
		}
	}
	if fmt.Sprint(ids) != "[4 3 2 1]" {
		t.Fatalf("activities duplicated, missing or leaked: %v", ids)
	}
}

func TestSyncSchemaMigratesExistingJobsAndGrants(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.ExecContext(ctx, `
CREATE TABLE jobs(id INTEGER PRIMARY KEY,type TEXT NOT NULL,status TEXT NOT NULL,payload TEXT NOT NULL,cursor TEXT NOT NULL,attempts INTEGER NOT NULL,max_attempts INTEGER NOT NULL,last_error TEXT NOT NULL,next_run_at INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
INSERT INTO jobs VALUES(1,'process_activity','retry','{"user_id":11,"activity_id":42}','{}',3,10,'old error',1,1,1);
CREATE TABLE strava_tokens(user_id INTEGER PRIMARY KEY,access_token TEXT NOT NULL,refresh_token TEXT NOT NULL,expires_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
INSERT INTO strava_tokens VALUES(11,'fake','refresh',2000000000,1);`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.InitSchema(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListUserJobs(ctx, 11, true, 10)
	if err != nil || len(rows) != 1 || rows[0].ActivityID != 42 || rows[0].Attempts != 3 {
		t.Fatalf("lost existing job: %v %v", rows, err)
	}
	token, err := s.GetStravaToken(ctx, 11)
	if err != nil || token.AccessToken != "fake" || token.Scopes != "" {
		t.Fatalf("changed existing grant: %+v %v", token, err)
	}
}
