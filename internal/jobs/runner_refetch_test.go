package jobs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weirdstats/internal/ingest"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type sourceCheckingProcessor struct {
	store *storage.Store
	seen  bool
}

func (p *sourceCheckingProcessor) Process(ctx context.Context, activityID int64) error {
	activity, err := p.store.GetActivity(ctx, activityID)
	if err != nil {
		return err
	}
	p.seen = activity.DeviceWatts != nil && *activity.DeviceWatts && activity.PowerSourceChecked
	return nil
}

func TestRefreshJobRefetchesBeforeProcessing(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(ctx, storage.StravaToken{UserID: 11, AccessToken: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertActivity(ctx, storage.Activity{ID: 42, UserID: 11, Type: "Ride", Name: "TTT", StartTime: time.Now(), StreamsFetched: true}, nil); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/activities/42":
			fmt.Fprint(w, `{"id":42,"name":"TTT","type":"Ride","start_date":"2026-09-20T09:00:00Z","device_watts":true}`)
		case "/api/activities/42/streams":
			fmt.Fprint(w, `{"latlng":{"data":[[48,11],[48,11.001]]},"time":{"data":[0,10]},"watts":{"data":[200,210]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	if err := EnqueueRefreshActivity(ctx, store, 42, 11); err != nil {
		t.Fatal(err)
	}
	processor := &sourceCheckingProcessor{store: store}
	runner := Runner{Store: store, Ingestor: &ingest.Ingestor{Store: store, Strava: &strava.Client{BaseURL: upstream.URL + "/api"}}, Processor: processor}
	worked, err := runner.ProcessNext(ctx)
	if err != nil || !worked || !processor.seen {
		t.Fatalf("job did not refetch before processing: worked=%v seen=%v err=%v", worked, processor.seen, err)
	}
}
