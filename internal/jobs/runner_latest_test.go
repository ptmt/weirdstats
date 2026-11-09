package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"weirdstats/internal/ingest"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type importingLatestProcessor struct{ ingestor *ingest.Ingestor }

func (p importingLatestProcessor) Process(ctx context.Context, id int64) error {
	return p.ingestor.EnsureActivity(ctx, id)
}

func TestLatestQueueActuallyImportsMultipleActivities(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/athlete/activities":
			fmt.Fprint(w, `[{"id":3,"start_date":"2026-11-08T12:00:00Z"},{"id":2,"start_date":"2026-11-07T12:00:00Z"},{"id":1,"start_date":"2026-11-06T12:00:00Z"}]`)
		case "/activities/1", "/activities/2", "/activities/3":
			var id int
			fmt.Sscanf(r.URL.Path, "/activities/%d", &id)
			fmt.Fprintf(w, `{"id":%d,"type":"Ride","name":"new ride","start_date":"2026-11-08T12:00:00Z"}`, id)
		case "/activities/1/streams", "/activities/2/streams", "/activities/3/streams":
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	ingestor := &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL}}
	runner := Runner{Store: store, Ingestor: ingestor, Processor: importingLatestProcessor{ingestor}}
	if err = store.RequestLatestSync(ctx, 11); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 4; run++ {
		if worked, err := runner.ProcessNext(ctx); err != nil || !worked {
			t.Fatalf("run %d: %v %v", run, worked, err)
		}
	}
	for _, id := range []int64{1, 2, 3} {
		a, err := store.GetActivityForUser(ctx, 11, id)
		fetched, fetchErr := store.ActivityStreamsFetched(ctx, id)
		if err != nil || fetchErr != nil || !fetched {
			t.Fatalf("not imported %d: %+v %v streams=%v %v", id, a, err, fetched, fetchErr)
		}
	}
	if worked, err := runner.ProcessNext(ctx); err != nil || worked {
		t.Fatalf("unexpected extra work %v %v", worked, err)
	}
}

func TestLatestDiscoversAllMissingAcrossDurablePages(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 120} {
		if _, err = store.InsertActivity(ctx, storage.Activity{ID: id, UserID: 11, Type: "Ride", Name: "test", StartTime: time.Unix(1000+id, 0)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/athlete/activities" {
			t.Errorf("discovery fetched detail: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls++
		var page, per int
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		fmt.Sscan(r.URL.Query().Get("per_page"), &per)
		if r.URL.Query().Get("before") == "" || r.URL.Query().Get("after") == "" {
			t.Error("scan is not anchored")
		}
		var rows []map[string]any
		for id := 120 - (page-1)*per; id > 0 && len(rows) < per; id-- {
			rows = append(rows, map[string]any{"id": id, "start_date": time.Unix(1000+int64(id), 0).UTC().Format(time.RFC3339)})
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer upstream.Close()
	id, err := store.CreateJob(ctx, storage.Job{Type: JobTypeSyncLatest, UserID: 11, Payload: `{"user_id":11}`})
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Store: store, Ingestor: &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL}}}
	for run := 0; run < 3; run++ {
		// Simulate restart: claim and handle only discovery, not the child jobs.
		if err = store.MarkJobQueued(ctx, id, latestCursor(t, store, id), time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		job, err := store.ClaimJob(ctx, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if job.ID != id {
			t.Fatalf("expected discovery, got %+v", job)
		}
		before := calls
		if err = runner.handleSyncLatest(ctx, job); err != nil {
			t.Fatal(err)
		}
		if calls-before != 1 {
			t.Fatalf("unbounded page calls: %d", calls-before)
		}
	}
	rows, err := store.ListJobs(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, job := range rows {
		if job.Type == JobTypeProcessActivity {
			if seen[job.ActivityID] {
				t.Fatal("duplicate job")
			}
			seen[job.ActivityID] = true
		} else if job.ID == id && job.Status != "completed" {
			t.Fatalf("unfinished: %+v", job)
		}
	}
	if len(seen) != 118 || seen[1] || seen[120] {
		t.Fatalf("missing gap activities: %d", len(seen))
	}
}
func TestLatestRateLimitsPreserveCursorAndScheduleWithoutAttempts(t *testing.T) {
	for _, status := range []int{429, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx := context.Background()
			store, err := storage.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.InitSchema(ctx); err != nil {
				t.Fatal(err)
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
				w.Header().Set("X-ReadRateLimit-Usage", "1,1000")
				w.WriteHeader(status)
			}))
			defer upstream.Close()
			cursor := `{"page":3,"enqueued":100,"after_unix":100,"before_unix":200}`
			_, err = store.CreateJob(ctx, storage.Job{Type: JobTypeSyncLatest, UserID: 11, Payload: `{"user_id":11}`, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			runner := Runner{Store: store, Ingestor: &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL}}}
			if _, err = runner.ProcessNext(ctx); err != nil {
				t.Fatal(err)
			}
			rows, _ := store.ListJobs(ctx, 10)
			job := rows[0]
			var saved SyncLatestCursor
			json.Unmarshal([]byte(job.Cursor), &saved)
			if saved.Page != 3 || saved.BeforeUnix != 200 || saved.AfterUnix != 100 || job.Attempts != 0 {
				t.Fatalf("lost progress: %+v", job)
			}
			if status == 429 {
				if job.Status != "waiting" || !job.NextRunAt.After(time.Now().UTC().Truncate(24*time.Hour).Add(24*time.Hour)) {
					t.Fatalf("daily limit not respected: %+v", job)
				}
			} else if job.Status != "blocked" || job.ErrorCode != "authorization_required" {
				t.Fatalf("not reconnectable: %+v", job)
			}
			if worked, err := runner.ProcessNext(ctx); err != nil || worked || calls != 1 {
				t.Fatalf("retried during wait/block: %v %v calls=%d", worked, err, calls)
			}
		})
	}
}

func latestCursor(t *testing.T, s *storage.Store, id int64) string {
	t.Helper()
	rows, err := s.ListJobs(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range rows {
		if j.ID == id {
			return j.Cursor
		}
	}
	t.Fatal("job missing")
	return ""
}
