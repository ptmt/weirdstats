package maps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestConsentCheckedBeforeEachExternalRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"elements":[]}`) }))
	defer server.Close()
	client := &OverpassClient{BaseURL: server.URL, DisableCache: true}
	denied := errors.New("consent withdrawn")
	allowed := true
	ctx := WithAccessCheck(context.Background(), func(context.Context) error {
		if !allowed {
			return denied
		}
		return nil
	})
	if _, err := client.NearbyFeaturesContext(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	allowed = false
	if _, err := client.NearbyFeaturesContext(ctx, 3, 4); !errors.Is(err, denied) {
		t.Fatalf("expected consent error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("made %d requests after consent changed", calls.Load())
	}
}
