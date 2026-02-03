package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Subscription struct {
	ID          int64  `json:"id"`
	CallbackURL string `json:"callback_url"`
}

type SubscriptionAction string

const (
	SubscriptionExists    SubscriptionAction = "exists"
	SubscriptionCreated   SubscriptionAction = "created"
	SubscriptionRecreated SubscriptionAction = "recreated"
	SubscriptionMismatch  SubscriptionAction = "mismatch"
)

var (
	ErrSubscriptionMismatch  = errors.New("subscription callback url mismatch")
	ErrMultipleSubscriptions = errors.New("multiple subscriptions returned")
)

type WebhookClient struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

func (c *WebhookClient) EnsureSubscription(ctx context.Context, callbackURL, verifyToken string, replace bool) (SubscriptionAction, *Subscription, error) {
	if err := c.validateCredentials(); err != nil {
		return "", nil, err
	}
	if callbackURL == "" {
		return "", nil, fmt.Errorf("callback url required")
	}
	if verifyToken == "" {
		return "", nil, fmt.Errorf("verify token required")
	}

	subscriptions, err := c.ListSubscriptions(ctx)
	if err != nil {
		return "", nil, err
	}

	if len(subscriptions) == 0 {
		sub, err := c.CreateSubscription(ctx, callbackURL, verifyToken)
		if err != nil {
			return "", nil, err
		}
		return SubscriptionCreated, sub, nil
	}

	if len(subscriptions) > 1 {
		return "", nil, fmt.Errorf("%w: %d", ErrMultipleSubscriptions, len(subscriptions))
	}

	current := subscriptions[0]
	if normalizeCallbackURL(current.CallbackURL) == normalizeCallbackURL(callbackURL) {
		return SubscriptionExists, &current, nil
	}

	if !replace {
		return SubscriptionMismatch, &current, fmt.Errorf("%w: existing=%q desired=%q", ErrSubscriptionMismatch, current.CallbackURL, callbackURL)
	}

	if err := c.DeleteSubscription(ctx, current.ID); err != nil {
		return "", &current, err
	}
	sub, err := c.CreateSubscription(ctx, callbackURL, verifyToken)
	if err != nil {
		return "", &current, err
	}
	return SubscriptionRecreated, sub, nil
}

func (c *WebhookClient) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	if err := c.validateCredentials(); err != nil {
		return nil, err
	}
	endpoint, err := c.buildURL("/push_subscriptions", true)
	if err != nil {
		return nil, err
	}

	logRequest(http.MethodGet, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("strava list subscriptions error %d: %s", resp.StatusCode, string(body))
	}

	var payload []Subscription
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *WebhookClient) CreateSubscription(ctx context.Context, callbackURL, verifyToken string) (*Subscription, error) {
	if err := c.validateCredentials(); err != nil {
		return nil, err
	}
	if callbackURL == "" {
		return nil, fmt.Errorf("callback url required")
	}
	if verifyToken == "" {
		return nil, fmt.Errorf("verify token required")
	}

	endpoint, err := c.buildURL("/push_subscriptions", false)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("callback_url", callbackURL)
	form.Set("verify_token", verifyToken)

	logRequest(http.MethodPost, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("strava create subscription error %d: %s", resp.StatusCode, string(body))
	}

	var payload Subscription
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.ID == 0 {
		return nil, fmt.Errorf("create subscription response missing id")
	}
	if payload.CallbackURL == "" {
		payload.CallbackURL = callbackURL
	}
	return &payload, nil
}

func (c *WebhookClient) DeleteSubscription(ctx context.Context, id int64) error {
	if err := c.validateCredentials(); err != nil {
		return err
	}
	if id <= 0 {
		return fmt.Errorf("subscription id required")
	}
	path := "/push_subscriptions/" + strconv.FormatInt(id, 10)
	endpoint, err := c.buildURL(path, true)
	if err != nil {
		return err
	}

	logRequest(http.MethodDelete, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("strava delete subscription error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *WebhookClient) buildURL(path string, includeCredentials bool) (string, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://www.strava.com/api/v3"
	}
	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return "", err
	}
	if !includeCredentials {
		return endpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", c.ClientID)
	query.Set("client_secret", c.ClientSecret)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *WebhookClient) validateCredentials() error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("missing strava client credentials")
	}
	return nil
}

func (c *WebhookClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func normalizeCallbackURL(raw string) string {
	return strings.TrimRight(raw, "/")
}
