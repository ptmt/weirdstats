package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"weirdstats/internal/config"
	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/maps"
	"weirdstats/internal/processor"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
	"weirdstats/internal/web"
	"weirdstats/internal/webhook"
	"weirdstats/internal/worker"
)

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(context.Background()); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	seedStravaToken(store, cfg)

	stravaClient := &strava.Client{
		BaseURL:     cfg.StravaBaseURL,
		AccessToken: cfg.StravaAccessToken,
	}
	if cfg.StravaRefreshToken != "" || (cfg.StravaClientID != "" && cfg.StravaClientSecret != "") {
		stravaClient.TokenSource = &strava.RefreshTokenSource{
			Store:        store,
			UserID:       1,
			ClientID:     cfg.StravaClientID,
			ClientSecret: cfg.StravaClientSecret,
			BaseURL:      cfg.StravaAuthBaseURL,
		}
	}
	ingestor := &ingest.Ingestor{Store: store, Strava: stravaClient}
	overpassClient := &maps.OverpassClient{
		BaseURL:    cfg.OverpassURL,
		MirrorURLs: cfg.OverpassURLs,
		Timeout:    time.Duration(cfg.OverpassTimeoutSec) * time.Second,
		CacheTTL:   time.Duration(cfg.OverpassCacheHours) * time.Hour,
	}

	stopOpts := gps.StopOptions{SpeedThreshold: 0.5, MinDuration: time.Minute}
	var mapAPI maps.API = overpassClient
	statsProcessor := &processor.StopStatsProcessor{
		Store:   store,
		MapAPI:  mapAPI,
		Options: stopOpts,
	}
	pipeline := &processor.PipelineProcessor{Ingest: ingestor, Stats: statsProcessor}
	queueWorker := &worker.Worker{Store: store, Processor: pipeline}

	webServer, err := web.NewServer(store, ingestor, mapAPI, overpassClient, stopOpts, web.StravaConfig{
		ClientID:     cfg.StravaClientID,
		ClientSecret: cfg.StravaClientSecret,
		AuthBaseURL:  cfg.StravaAuthBaseURL,
		RedirectURL:  cfg.StravaRedirectURL,
	})
	if err != nil {
		log.Fatalf("load templates: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.FileServerFS(web.StaticFS))
	mux.HandleFunc("/", webServer.Landing)
	mux.HandleFunc("/connect/strava", webServer.ConnectStrava)
	mux.HandleFunc("/connect/strava/callback", webServer.StravaCallback)
	mux.HandleFunc("/profile", webServer.Profile)
	mux.HandleFunc("/profile/", webServer.Profile)
	mux.HandleFunc("/profile/settings", webServer.Settings)
	mux.HandleFunc("/activity/", webServer.Activity)
	mux.HandleFunc("/admin", webServer.Admin)
	mux.HandleFunc("/admin/", webServer.Admin)
	mux.Handle("/webhook", &webhook.Handler{
		Store:         store,
		VerifyToken:   cfg.StravaVerifyToken,
		SigningSecret: cfg.StravaWebhookSecret,
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server error: %v", err)
			stop()
		}
	}()

	go runWorker(ctx, queueWorker, time.Duration(cfg.WorkerPollIntervalMS)*time.Millisecond)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func seedStravaToken(store *storage.Store, cfg config.Config) {
	if cfg.StravaRefreshToken == "" && cfg.StravaAccessToken == "" {
		return
	}
	// Don't overwrite if user already has a token from OAuth
	existing, err := store.GetStravaToken(context.Background(), 1)
	if err == nil && existing.AccessToken != "" {
		log.Printf("Skipping token seed - OAuth token already exists")
		return
	}
	expiresAt := time.Now().Add(-time.Minute)
	if cfg.StravaAccessExpiry > 0 {
		expiresAt = time.Unix(cfg.StravaAccessExpiry, 0)
	}
	if err := store.UpsertStravaToken(context.Background(), storage.StravaToken{
		UserID:       1,
		AccessToken:  cfg.StravaAccessToken,
		RefreshToken: cfg.StravaRefreshToken,
		ExpiresAt:    expiresAt,
	}); err != nil {
		log.Printf("seed strava token: %v", err)
	}
}

func runWorker(ctx context.Context, queueWorker *worker.Worker, idleDelay time.Duration) {
	if idleDelay <= 0 {
		idleDelay = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := queueWorker.ProcessNext(ctx)
		if err != nil {
			log.Printf("worker error: %v", err)
		}
		if !processed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(idleDelay):
			}
		}
	}
}
