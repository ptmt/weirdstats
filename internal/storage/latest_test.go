package storage

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLatestRequestsCoalesceConcurrentOwnersAndCooldown(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RequestLatestSync(ctx, 11); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rows, _ := s.ListJobs(ctx, 100)
	if len(rows) != 1 {
		t.Fatalf("duplicate requests: %d", len(rows))
	}
	if err = s.MarkJobCompleted(ctx, rows[0].ID, `{"before_unix":12345}`); err != nil {
		t.Fatal(err)
	}
	if err = s.RequestLatestSync(ctx, 11); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListJobs(ctx, 100)
	if len(rows) != 1 {
		t.Fatal("cooldown ignored")
	}
	if err = s.RequestLatestSync(ctx, 22); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListJobs(ctx, 100)
	if len(rows) != 2 {
		t.Fatal("owner blocked another owner")
	}
	after, err := s.LatestSyncAfter(ctx, 11)
	if err != nil || after != 12344 {
		t.Fatalf("bad watermark %d %v", after, err)
	}
	// Repeating a page after a restart must not create another child job.
	parent, err := s.ClaimJob(ctx, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	child := Job{Type: "process_activity", UserID: 22, ActivityID: 42, ParentID: parent.ID}
	for n := 0; n < 2; n++ {
		if err = s.DeferJob(ctx, parent.ID, "{}", "running", "", "", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = s.EnqueueSyncPage(ctx, parent, []Job{child}, "{}", "queued", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ = s.ListJobs(ctx, 100)
	if len(rows) != 3 {
		t.Fatalf("duplicate child: %+v", rows)
	}
}
