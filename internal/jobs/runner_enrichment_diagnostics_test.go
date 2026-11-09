package jobs

import (
	"bytes"
	"context"
	"log"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"weirdstats/internal/maps"
	"weirdstats/internal/storage"
)

func TestEnrichmentLogsSafeCauseAndRetainsSafeUserMessage(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	for _, test := range []struct {
		err   error
		cause string
	}{
		{&url.Error{Op: "Get", URL: "https://secret.invalid/?data=52.123,13.456&token=secret", Err: syscall.ECONNREFUSED}, "cause=connection_refused http_status=0"},
		{&maps.ResponseError{StatusCode: 503}, "cause=http_status http_status=503"},
	} {
		logs.Reset()
		id, err := store.CreateJob(ctx, storage.Job{Type: JobTypeEnrichActivity, UserID: 11, ActivityID: 42, Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		runner := Runner{Store: store}
		if err = runner.markJobRetry(ctx, storage.Job{ID: id, Type: JobTypeEnrichActivity, UserID: 11, MaxAttempts: 1}, SyncSinceCursor{}, test.err); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), test.cause) {
			t.Fatalf("cause lost: %s", logs.String())
		}
		for _, secret := range []string{"secret", "52.123", "13.456", "https://"} {
			if strings.Contains(logs.String(), secret) {
				t.Fatalf("private log: %s", logs.String())
			}
		}
	}
}
