Development

Prereqs
- Go 1.22+

Run
1) Optional: create `.env` with any of these keys:
   - DATABASE_PATH (default: weirdstats.db)
   - SERVER_ADDR (default: :8080)
   - STRAVA_BASE_URL (default: https://www.strava.com/api/v3)
   - STRAVA_AUTH_BASE_URL (default: https://www.strava.com)
   - WORKER_POLL_INTERVAL_MS (default: 2000)
   - STRAVA_CLIENT_ID
   - STRAVA_CLIENT_SECRET
   - STRAVA_REFRESH_TOKEN
   - STRAVA_ACCESS_TOKEN
   - STRAVA_ACCESS_TOKEN_EXPIRES_AT (unix seconds)
   - STRAVA_VERIFY_TOKEN
   - STRAVA_WEBHOOK_SECRET
   - MAPS_API_KEY
2) Start the server:
   - go run ./cmd/weirdstats
3) Validate:
   - curl http://localhost:8080/healthz
4) Run tests:
   - go test ./...

Notes
- Without Strava credentials, the server still runs and the webhook will enqueue but activity fetching will fail when the worker processes items.
- Use a SQLite viewer or `sqlite3` to inspect `weirdstats.db` if needed.
- The server runs a background worker loop to process the queue (see `SPEC.md` for workflow).
- Webhook verification uses GET `/webhook?hub.challenge=...&hub.verify_token=...` (see `SPEC.md`).
- POST `/webhook` checks `X-Strava-Signature` when `STRAVA_WEBHOOK_SECRET` is set (see `SPEC.md`).
