package maps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOverpassClient_RequestsAndParses(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("data") == "" {
			t.Fatalf("expected data query param")
		}
		atomic.AddInt32(&requestCount, 1)
		resp := overpassResponse{
			Elements: []overpassElement{
				{Lat: 40.0, Lon: -73.0, Tags: map[string]string{"highway": "traffic_signals", "name": "Main"}},
				{Lat: 40.1, Lon: -73.1, Tags: map[string]string{"amenity": "cafe", "name": "Cafe XYZ"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OverpassClient{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		DisableCache: true,
	}

	features, err := client.NearbyFeatures(40.0, -73.0)
	if err != nil {
		t.Fatalf("NearbyFeatures error: %v", err)
	}
	if len(features) != 1 || features[0].Type != FeatureTrafficLight {
		t.Fatalf("unexpected features: %+v", features)
	}

	ctx := context.Background()
	pois, err := client.FetchPOIs(ctx, BBox{South: 1, West: 1, North: 2, East: 2}, true, true)
	if err != nil {
		t.Fatalf("FetchPOIs error: %v", err)
	}
	if len(pois) != 2 {
		t.Fatalf("expected 2 pois, got %d", len(pois))
	}
	if pois[0].Type != FeatureTrafficLight || pois[1].Type != FeatureCafe {
		t.Fatalf("unexpected poi types: %+v", pois)
	}

	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("expected 2 requests (no cache), got %d", got)
	}
}

func TestOverpassClient_RoundRobinMirrors(t *testing.T) {
	var firstHits, secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`overpass down`))
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		resp := overpassResponse{
			Elements: []overpassElement{{Lat: 1, Lon: 2, Tags: map[string]string{"highway": "traffic_signals"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer second.Close()

	client := &OverpassClient{
		MirrorURLs:   []string{first.URL, second.URL},
		HTTPClient:   first.Client(),
		MaxAttempts:  2,
		DisableCache: true,
	}

	features, err := client.NearbyFeatures(0, 0)
	if err != nil {
		t.Fatalf("round robin failed: %v", err)
	}
	if len(features) != 1 || features[0].Type != FeatureTrafficLight {
		t.Fatalf("unexpected features: %+v", features)
	}
	if atomic.LoadInt32(&firstHits) != 1 || atomic.LoadInt32(&secondHits) != 1 {
		t.Fatalf("expected 1 hit per mirror, got first=%d second=%d", firstHits, secondHits)
	}
}
