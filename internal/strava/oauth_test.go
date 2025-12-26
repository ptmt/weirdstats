package strava

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weirdstats/internal/storage"
)

func TestRefreshTokenSource(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	if err := store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:       1,
		AccessToken:  "",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"access-2","refresh_token":"refresh-2","expires_at":4102444800}`))
	}))
	defer server.Close()

	source := &RefreshTokenSource{
		Store:        store,
		UserID:       1,
		ClientID:     "id",
		ClientSecret: "secret",
		BaseURL:      server.URL,
	}

	token, err := source.GetAccessToken(ctx)
	if err != nil {
		t.Fatalf("get access token: %v", err)
	}
	if token != "access-2" {
		t.Fatalf("unexpected token: %s", token)
	}

	stored, err := store.GetStravaToken(ctx, 1)
	if err != nil {
		t.Fatalf("get stored token: %v", err)
	}
	if stored.AccessToken != "access-2" || stored.RefreshToken != "refresh-2" {
		t.Fatalf("unexpected stored tokens: %+v", stored)
	}
}
