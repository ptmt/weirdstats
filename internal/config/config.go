package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DatabasePath         string
	ServerAddr           string
	StravaAccessToken    string
	StravaAccessExpiry   int64
	StravaRefreshToken   string
	StravaClientID       string
	StravaClientSecret   string
	StravaBaseURL        string
	StravaAuthBaseURL    string
	StravaRedirectURL    string
	StravaVerifyToken    string
	StravaWebhookSecret  string
	MapsAPIKey           string
	WorkerPollIntervalMS int
}

func Load(path string) (Config, error) {
	cfg := Config{
		ServerAddr:           ":8080",
		StravaBaseURL:        "https://www.strava.com/api/v3",
		StravaAuthBaseURL:    "https://www.strava.com",
		WorkerPollIntervalMS: 2000,
	}

	if path != "" {
		if err := loadDotEnv(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
	}

	cfg.DatabasePath = getenv("DATABASE_PATH", "weirdstats.db")
	cfg.ServerAddr = getenv("SERVER_ADDR", cfg.ServerAddr)
	cfg.StravaAccessToken = os.Getenv("STRAVA_ACCESS_TOKEN")
	cfg.StravaRefreshToken = os.Getenv("STRAVA_REFRESH_TOKEN")
	cfg.StravaClientID = os.Getenv("STRAVA_CLIENT_ID")
	cfg.StravaClientSecret = os.Getenv("STRAVA_CLIENT_SECRET")
	cfg.StravaBaseURL = getenv("STRAVA_BASE_URL", cfg.StravaBaseURL)
	cfg.StravaAuthBaseURL = getenv("STRAVA_AUTH_BASE_URL", cfg.StravaAuthBaseURL)
	cfg.StravaRedirectURL = os.Getenv("STRAVA_REDIRECT_URL")
	cfg.StravaVerifyToken = os.Getenv("STRAVA_VERIFY_TOKEN")
	cfg.StravaWebhookSecret = os.Getenv("STRAVA_WEBHOOK_SECRET")
	cfg.MapsAPIKey = os.Getenv("MAPS_API_KEY")

	if v := os.Getenv("WORKER_POLL_INTERVAL_MS"); v != "" {
		if err := parseInt(&cfg.WorkerPollIntervalMS, v); err != nil {
			return Config{}, fmt.Errorf("WORKER_POLL_INTERVAL_MS: %w", err)
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
