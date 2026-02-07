package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"weirdstats/internal/config"
	"weirdstats/internal/gps"
	"weirdstats/internal/ingest"
	"weirdstats/internal/jobs"
	"weirdstats/internal/maps"
	"weirdstats/internal/processor"
	"weirdstats/internal/rules"
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
	logStartupConfig(cfg)

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
		Store:    store,
		MapAPI:   mapAPI,
		Overpass: overpassClient,
		Options:  stopOpts,
	}
	rulesProcessor := &processor.RulesProcessor{
		Store:    store,
		Registry: rules.DefaultRegistry(),
	}
	pipeline := &processor.PipelineProcessor{Ingest: ingestor, Stats: statsProcessor, Rules: rulesProcessor}
	queueWorker := &worker.Worker{Store: store, Processor: pipeline}
	jobRunner := &jobs.Runner{
		Store:        store,
		Ingestor:     ingestor,
		Processor:    pipeline,
		PollInterval: time.Duration(cfg.WorkerPollIntervalMS) * time.Millisecond,
		StaleAfter:   10 * time.Minute,
	}

	webServer, err := web.NewServer(store, ingestor, mapAPI, overpassClient, stopOpts, web.StravaConfig{
		ClientID:        cfg.StravaClientID,
		ClientSecret:    cfg.StravaClientSecret,
		AuthBaseURL:     cfg.StravaAuthBaseURL,
		RedirectURL:     cfg.StravaRedirectURL,
		InitialSyncDays: cfg.StravaInitialSyncDays,
	})
	if err != nil {
		log.Fatalf("load templates: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", webServer.Landing)
	mux.HandleFunc("/connect/strava", webServer.ConnectStrava)
	mux.HandleFunc("/connect/strava/callback", webServer.StravaCallback)
	mux.HandleFunc("/activities", webServer.Activities)
	mux.HandleFunc("/activities/", webServer.Activities)
	mux.HandleFunc("/activities/settings", webServer.Settings)
	mux.HandleFunc("/api/rules/metadata", webServer.RulesMetadata)
	mux.HandleFunc("/activity/", webServer.Activity)
	mux.HandleFunc("/admin", webServer.Admin)
	mux.HandleFunc("/admin/", webServer.Admin)
	mux.HandleFunc("/stats/users", webServer.UsersCount)
	mux.Handle("/static/", http.StripPrefix("/static/", web.StaticHandler()))
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

	listener, err := net.Listen("tcp", cfg.ServerAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server error: %v", err)
			stop()
		}
	}()

	go ensureWebhookSubscription(ctx, cfg)
	go runWorker(ctx, queueWorker, time.Duration(cfg.WorkerPollIntervalMS)*time.Millisecond)
	go runJobRunner(ctx, jobRunner)

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

	rateLimitBackoff := time.Duration(0)
	const (
		rateLimitBackoffStart = 15 * time.Second
		rateLimitBackoffMax   = 10 * time.Minute
	)

	nextBackoff := func(current time.Duration) time.Duration {
		if current <= 0 {
			return rateLimitBackoffStart
		}
		next := current * 2
		if next > rateLimitBackoffMax {
			return rateLimitBackoffMax
		}
		return next
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := queueWorker.ProcessNext(ctx)
		if err != nil {
			if strava.IsRateLimited(err) {
				fallback := nextBackoff(rateLimitBackoff)
				rateLimitBackoff = fallback
				backoff := fallback
				if retryAfter, ok := strava.RateLimitBackoff(err); ok && retryAfter > 0 {
					backoff = retryAfter
				}
				log.Printf("worker rate limited; backing off for %s; %v", backoff, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				continue
			}
			log.Printf("worker error: %v", err)
		} else if processed {
			rateLimitBackoff = 0
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

func runJobRunner(ctx context.Context, runner *jobs.Runner) {
	idleDelay := runner.PollInterval
	if idleDelay <= 0 {
		idleDelay = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := runner.ProcessNext(ctx)
		if err != nil {
			log.Printf("job runner error: %v", err)
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

func logStartupConfig(cfg config.Config) {
	hasClientID := cfg.StravaClientID != ""
	hasClientSecret := cfg.StravaClientSecret != ""
	hasRefreshToken := cfg.StravaRefreshToken != ""
	hasAccessToken := cfg.StravaAccessToken != ""
	hasBaseURL := cfg.BaseURL != ""
	hasVerifyToken := cfg.StravaVerifyToken != ""
	hasWebhookSecret := cfg.StravaWebhookSecret != ""

	log.Printf("strava env: client_id=%t client_secret=%t refresh_token=%t access_token=%t",
		hasClientID, hasClientSecret, hasRefreshToken, hasAccessToken)
	log.Printf("webhook env: auto_register=%t base_url=%t verify_token=%t client_credentials=%t signing_secret=%t",
		cfg.StravaWebhookAutoRegister, hasBaseURL, hasVerifyToken, hasClientID && hasClientSecret, hasWebhookSecret)

	if cfg.StravaWebhookAutoRegister {
		var missing []string
		if !hasBaseURL {
			missing = append(missing, "BASE_URL")
		}
		if !hasVerifyToken {
			missing = append(missing, "STRAVA_VERIFY_TOKEN")
		}
		if !hasClientID {
			missing = append(missing, "STRAVA_CLIENT_ID")
		}
		if !hasClientSecret {
			missing = append(missing, "STRAVA_CLIENT_SECRET")
		}
		if len(missing) == 0 {
			log.Printf("webhook auto-register ready")
		} else {
			log.Printf("webhook auto-register missing envs: %s", strings.Join(missing, ", "))
		}
	}
}
