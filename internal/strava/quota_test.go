package strava

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"weirdstats/internal/storage"
)

func TestQuotaSharedAcrossUsersAndFactoryRestart(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: id, AccessToken: "fake"}); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
		w.Header().Set("X-ReadRateLimit-Usage", "1,1000")
		w.Write([]byte("[]"))
	}))
	defer server.Close()
	factory := &ClientFactory{Store: store, BaseURL: server.URL}
	client, err := factory.ClientForUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListActivities(ctx, time.Time{}, time.Time{}, 1, 100); err != nil {
		t.Fatal(err)
	}
	// A new factory also reads the persisted cooldown, rather than calling upstream.
	factory = &ClientFactory{Store: store, BaseURL: server.URL}
	client, err = factory.ClientForUser(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListActivities(ctx, time.Time{}, time.Time{}, 1, 100)
	if !IsRateLimited(err) {
		t.Fatalf("expected rate-limited wait, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one upstream call, got %d", calls.Load())
	}
	until, err := store.APICooldown(ctx, "strava")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Truncate(24 * time.Hour).Add(24*time.Hour + time.Second)
	if !until.Equal(want) {
		t.Fatalf("reset=%s, want %s", until, want)
	}
}

func TestQuotaWaitUsesLongerReset(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 1, 0, 0, time.UTC)
	info := RateLimitInfo{ReadLimitShort: 100, ReadUsageShort: 100, ReadLimitLong: 1000, ReadUsageLong: 1000, RetryAfter: time.Minute}
	if got := info.resetAt(now, true); !got.Equal(time.Date(2026, 9, 9, 0, 0, 1, 0, time.UTC)) {
		t.Fatal(got)
	}
}

func TestBackfillReservesInteractiveCapacity(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 1, AccessToken: "fake"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
		w.Header().Set("X-ReadRateLimit-Usage", "90,90")
		w.Write([]byte("[]"))
	}))
	defer server.Close()
	client, err := (&ClientFactory{Store: store, BaseURL: server.URL}).ClientForUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	backfill := storage.ContextWithBackfill(ctx, true)
	if _, err = client.ListActivities(backfill, time.Time{}, time.Time{}, 1, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListActivities(backfill, time.Time{}, time.Time{}, 2, 100); !IsRateLimited(err) {
		t.Fatalf("backfill was not paused: %v", err)
	}
	if _, err = client.ListActivities(ctx, time.Time{}, time.Time{}, 1, 1); err != nil {
		t.Fatalf("interactive request blocked: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
}
