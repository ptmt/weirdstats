package strava

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"weirdstats/internal/storage"
)

type ClientFactory struct {
	Store            *storage.Store
	BaseURL          string
	AuthBaseURL      string
	ClientID         string
	ClientSecret     string
	HTTPClient       *http.Client
	once             sync.Once
	sharedHTTPClient *http.Client
	refreshLocks     sync.Map
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
	if token.Scopes != "" && !CanReadActivities(token.Scopes) {
		return nil, &PermissionError{Scope: "activity:read"}
	}
	f.once.Do(func() {
		client := *defaultHTTPClient
		if f.HTTPClient != nil {
			client = *f.HTTPClient
		}
		if client.Timeout == 0 {
			client.Timeout = defaultHTTPClient.Timeout
		}
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.Transport = &quotaTransport{base: base, store: f.Store}
		f.sharedHTTPClient = &client
	})

	client := &Client{
		BaseURL:      f.BaseURL,
		HTTPClient:   f.sharedHTTPClient,
		Scopes:       token.Scopes,
		ConnectionID: token.ConnectionID,
	}
	client.CheckAccess = func(ctx context.Context) error {
		current, err := f.Store.GetStravaToken(ctx, userID)
		if err != nil {
			return storage.ErrConnectionChanged
		}
		if current.ConnectionID != token.ConnectionID {
			return storage.ErrConnectionChanged
		}
		return nil
	}
	if f.ClientID != "" && f.ClientSecret != "" && token.RefreshToken != "" {
		lock, _ := f.refreshLocks.LoadOrStore(userID, &sync.Mutex{})
		client.TokenSource = &RefreshTokenSource{
			Store:        f.Store,
			UserID:       userID,
			ClientID:     f.ClientID,
			ClientSecret: f.ClientSecret,
			BaseURL:      f.AuthBaseURL,
			HTTPClient:   f.sharedHTTPClient,
			RefreshMu:    lock.(*sync.Mutex),
		}
		return client, nil
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("missing access token for user %d", userID)
	}
	client.AccessToken = token.AccessToken
	return client, nil
}
