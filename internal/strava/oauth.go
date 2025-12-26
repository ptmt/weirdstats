package strava

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"weirdstats/internal/storage"
)

type TokenSource interface {
	GetAccessToken(ctx context.Context) (string, error)
}

type RefreshTokenSource struct {
	Store        *storage.Store
	UserID       int64
	ClientID     string
	ClientSecret string
	BaseURL      string
	HTTPClient   *http.Client
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

func (s *RefreshTokenSource) GetAccessToken(ctx context.Context) (string, error) {
	if s.Store == nil {
		return "", fmt.Errorf("token store not configured")
	}

	token, err := s.Store.GetStravaToken(ctx, s.UserID)
	if err != nil {
		return "", err
	}

	if token.AccessToken != "" && time.Now().Before(token.ExpiresAt.Add(-time.Minute)) {
		return token.AccessToken, nil
	}

	if token.RefreshToken == "" {
		return "", fmt.Errorf("missing refresh token")
	}

	updated, err := s.refresh(ctx, token.RefreshToken)
	if err != nil {
		return "", err
	}

	if updated.RefreshToken == "" {
		updated.RefreshToken = token.RefreshToken
	}

	if err := s.Store.UpsertStravaToken(ctx, storage.StravaToken{
		UserID:       token.UserID,
		AccessToken:  updated.AccessToken,
		RefreshToken: updated.RefreshToken,
		ExpiresAt:    time.Unix(updated.ExpiresAt, 0),
	}); err != nil {
		return "", err
	}

	return updated.AccessToken, nil
}

func (s *RefreshTokenSource) refresh(ctx context.Context, refreshToken string) (refreshResponse, error) {
	if s.ClientID == "" || s.ClientSecret == "" {
		return refreshResponse{}, fmt.Errorf("missing strava client credentials")
	}

	base := s.BaseURL
	if base == "" {
		base = "https://www.strava.com"
	}

	endpoint, err := url.JoinPath(base, "/oauth/token")
	if err != nil {
		return refreshResponse{}, err
	}

	form := url.Values{}
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return refreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return refreshResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return refreshResponse{}, fmt.Errorf("strava refresh error %d: %s", resp.StatusCode, string(body))
	}

	var payload refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return refreshResponse{}, err
	}

	if payload.AccessToken == "" {
		return refreshResponse{}, fmt.Errorf("refresh response missing access_token")
	}

	return payload, nil
}
