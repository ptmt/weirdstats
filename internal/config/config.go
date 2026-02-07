package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	BaseURL                   string
	DatabasePath              string
	ServerAddr                string
	StravaAccessToken         string
	StravaAccessExpiry        int64
	StravaRefreshToken        string
	StravaClientID            string
	StravaClientSecret        string
	StravaBaseURL             string
	StravaAuthBaseURL         string
	StravaRedirectURL         string
	StravaVerifyToken         string
	StravaWebhookSecret       string
	StravaWebhookCallbackURL  string
	StravaWebhookAutoRegister bool
	StravaWebhookAutoReplace  bool
	StravaInitialSyncDays     int
	MapsAPIKey                string
	OverpassURL               string
	OverpassURLs              []string
	OverpassTimeoutSec        int
	OverpassCacheHours        int
	WorkerPollIntervalMS      int
}

func Load(path string) (Config, error) {
	cfg := Config{
		ServerAddr:            ":8080",
		StravaBaseURL:         "https://www.strava.com/api/v3",
		StravaAuthBaseURL:     "https://www.strava.com",
		StravaInitialSyncDays: 30,
		WorkerPollIntervalMS:  2000,
	}

	if path != "" {
		if err := loadDotEnv(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
	}

	cfg.DatabasePath = getenv("DATABASE_PATH", "weirdstats.db")
	cfg.ServerAddr = getenv("SERVER_ADDR", cfg.ServerAddr)
	cfg.BaseURL = strings.TrimRight(os.Getenv("BASE_URL"), "/")
	cfg.StravaAccessToken = os.Getenv("STRAVA_ACCESS_TOKEN")
	cfg.StravaRefreshToken = os.Getenv("STRAVA_REFRESH_TOKEN")
	cfg.StravaClientID = os.Getenv("STRAVA_CLIENT_ID")
	cfg.StravaClientSecret = os.Getenv("STRAVA_CLIENT_SECRET")
	cfg.StravaBaseURL = getenv("STRAVA_BASE_URL", cfg.StravaBaseURL)
	cfg.StravaAuthBaseURL = getenv("STRAVA_AUTH_BASE_URL", cfg.StravaAuthBaseURL)
	cfg.StravaVerifyToken = os.Getenv("STRAVA_VERIFY_TOKEN")
	cfg.StravaWebhookSecret = os.Getenv("STRAVA_WEBHOOK_SECRET")
	if cfg.BaseURL != "" {
		cfg.StravaRedirectURL = joinURL(cfg.BaseURL, "/connect/strava/callback")
		cfg.StravaWebhookCallbackURL = joinURL(cfg.BaseURL, "/webhook")
	}
	cfg.MapsAPIKey = os.Getenv("MAPS_API_KEY")
	cfg.OverpassURL = os.Getenv("OVERPASS_URL")
	if v := os.Getenv("OVERPASS_URLS"); v != "" {
		cfg.OverpassURLs = splitAndTrim(v)
	}

	if v := os.Getenv("WORKER_POLL_INTERVAL_MS"); v != "" {
		if err := parseInt(&cfg.WorkerPollIntervalMS, v); err != nil {
			return Config{}, fmt.Errorf("WORKER_POLL_INTERVAL_MS: %w", err)
		}
	}
	if v := os.Getenv("STRAVA_INITIAL_SYNC_DAYS"); v != "" {
		if err := parseInt(&cfg.StravaInitialSyncDays, v); err != nil {
			return Config{}, fmt.Errorf("STRAVA_INITIAL_SYNC_DAYS: %w", err)
		}
	}
	if v := os.Getenv("STRAVA_WEBHOOK_AUTO_REGISTER"); v != "" {
		if err := parseBool(&cfg.StravaWebhookAutoRegister, v); err != nil {
			return Config{}, fmt.Errorf("STRAVA_WEBHOOK_AUTO_REGISTER: %w", err)
		}
	}
	if v := os.Getenv("STRAVA_WEBHOOK_AUTO_REPLACE"); v != "" {
		if err := parseBool(&cfg.StravaWebhookAutoReplace, v); err != nil {
			return Config{}, fmt.Errorf("STRAVA_WEBHOOK_AUTO_REPLACE: %w", err)
		}
	}
	if v := os.Getenv("OVERPASS_TIMEOUT_SECONDS"); v != "" {
		if err := parseInt(&cfg.OverpassTimeoutSec, v); err != nil {
			return Config{}, fmt.Errorf("OVERPASS_TIMEOUT_SECONDS: %w", err)
		}
	}
	if v := os.Getenv("OVERPASS_CACHE_HOURS"); v != "" {
		if err := parseInt(&cfg.OverpassCacheHours, v); err != nil {
			return Config{}, fmt.Errorf("OVERPASS_CACHE_HOURS: %w", err)
		}
	}
	if v := os.Getenv("STRAVA_ACCESS_TOKEN_EXPIRES_AT"); v != "" {
		if err := parseInt64(&cfg.StravaAccessExpiry, v); err != nil {
			return Config{}, fmt.Errorf("STRAVA_ACCESS_TOKEN_EXPIRES_AT: %w", err)
		}
	}

	return cfg, nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		_ = os.Setenv(key, strings.Trim(value, `"`))
	}

	return scanner.Err()
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseInt(target *int, value string) error {
	var parsed int
	_, err := fmt.Sscanf(value, "%d", &parsed)
	if err != nil {
		return err
	}
	*target = parsed
	return nil
}

func parseInt64(target *int64, value string) error {
	var parsed int64
	_, err := fmt.Sscanf(value, "%d", &parsed)
	if err != nil {
		return err
	}
	*target = parsed
	return nil
}

func parseBool(target *bool, value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	*target = parsed
	return nil
}

func splitAndTrim(value string) []string {
	parts := strings.Split(value, ",")
	var out []string
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func joinURL(base, path string) string {
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}
