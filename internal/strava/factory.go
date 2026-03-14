package strava

import (
	"context"
	"fmt"
	"net/http"

	"weirdstats/internal/storage"
)

type ClientFactory struct {
	Store        *storage.Store
	BaseURL      string
	AuthBaseURL  string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

func (f *ClientFactory) ClientForUser(ctx context.Context, userID int64) (*Client, error) {
	if f == nil {
		return nil, fmt.Errorf("strava client factory not configured")
	}
	if f.Store == nil {
		return nil, fmt.Errorf("strava token store not configured")
	}
	if userID == 0 {
		return nil, fmt.Errorf("strava user id required")
	}

	token, err := f.Store.GetStravaToken(ctx, userID)
	if err != nil {
		return nil, err
	}

	client := &Client{
		BaseURL:    f.BaseURL,
		HTTPClient: f.HTTPClient,
	}
	if f.ClientID != "" && f.ClientSecret != "" && token.RefreshToken != "" {
		client.TokenSource = &RefreshTokenSource{
			Store:        f.Store,
			UserID:       userID,
			ClientID:     f.ClientID,
			ClientSecret: f.ClientSecret,
			BaseURL:      f.AuthBaseURL,
			HTTPClient:   f.HTTPClient,
		}
		return client, nil
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("missing access token for user %d", userID)
	}
	client.AccessToken = token.AccessToken
	return client, nil
}
