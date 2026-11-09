package maps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"
	"time"
)

type failingTransport struct{ calls int }

func (f *failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	if r.URL.Host == "refused.invalid" {
		return nil, syscall.ECONNREFUSED
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestOverpassTransportFallbackIsBounded(t *testing.T) {
	hits := 0
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{"elements":[]}`)) }))
	defer mirror.Close()
	transport := &failingTransport{}
	c := &OverpassClient{MirrorURLs: []string{"http://refused.invalid", mirror.URL}, HTTPClient: &http.Client{Transport: transport}, MaxAttempts: 2, BackoffBase: time.Nanosecond}
	if _, err := c.runQueryWithRetry(context.Background(), "nonpersonal"); err != nil {
		t.Fatalf("connection refused did not fall back: %v", err)
	}
	if transport.calls != 2 || hits != 1 {
		t.Fatalf("unbounded calls: %d %d", transport.calls, hits)
	}
}

func TestOverpassNeverRetriesOrMirrors429(t *testing.T) {
	hits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(429) }))
	defer provider.Close()
	c := &OverpassClient{MirrorURLs: []string{provider.URL, provider.URL}, MaxAttempts: 5, BackoffBase: time.Nanosecond}
	if _, err := c.runQueryWithRetry(context.Background(), "nonpersonal"); err == nil {
		t.Fatal("expected rate limit")
	}
	if hits != 1 {
		t.Fatalf("amplified 429: %d requests", hits)
	}
}

func TestOverpassDoesNotFallbackOnPermanentOrInvalidResponses(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			hits := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(status)
				w.Write([]byte("invalid secret response"))
			}))
			defer provider.Close()
			c := &OverpassClient{MirrorURLs: []string{provider.URL, provider.URL}, MaxAttempts: 5, BackoffBase: time.Nanosecond}
			if _, err := c.runQueryWithRetry(context.Background(), "nonpersonal"); err == nil {
				t.Fatal("expected error")
			}
			if hits != 1 {
				t.Fatalf("permanent/invalid response amplified: %d", hits)
			}
		})
	}
}

func TestOverpassUnavailableMirrorsRespectAttemptBudget(t *testing.T) {
	hits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(503) }))
	defer provider.Close()
	c := &OverpassClient{MirrorURLs: []string{provider.URL, provider.URL}, MaxAttempts: 2, BackoffBase: time.Nanosecond}
	if _, err := c.runQueryWithRetry(context.Background(), "nonpersonal"); err == nil {
		t.Fatal("expected error")
	}
	if hits != 2 {
		t.Fatalf("budget not respected: %d", hits)
	}
}

func TestOverpassCanceledDoesNotQueryMirror(t *testing.T) {
	hits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(503) }))
	defer provider.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &OverpassClient{MirrorURLs: []string{provider.URL, provider.URL}}
	if _, err := c.runQueryWithRetry(ctx, "nonpersonal"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if hits != 0 {
		t.Fatalf("queried after cancellation: %d", hits)
	}
}

func TestOverpassSafeCauseNeverContainsProviderText(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://secret.invalid/?data=52.123,13.456&token=secret", Err: syscall.ECONNREFUSED}
	cause, status := SafeError(err)
	if cause != "connection_refused" || status != 0 {
		t.Fatalf("cause=%q status=%d", cause, status)
	}
	cause, status = SafeError(errors.New("secret response body coordinates token"))
	if cause != "unknown" || status != 0 {
		t.Fatalf("unsafe cause=%q status=%d", cause, status)
	}
}
