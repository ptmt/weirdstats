# Explicit Strava catch-up sync

## Root cause and production observations (read-only)

Feed refresh was GET-only (saved data). The admin `sync_latest` imported **one** newest activity, leaving any older gap untouched. This change replaces that job implementation with durable discovery rather than relabeling reload.

Read-only Docker inspection on 2026-11-09 found auto-register enabled, callback `https://weirdstats.com/webhook`, client credentials and verify token configured. Current container startup logged subscription **329186 exists** with that callback. The last 3,000 retained log lines contained no incoming `strava webhook:` entries; this is not proof that Strava delivered no events. No subscription mutation or live Strava metadata request was needed/performed.

SQLite opened with `mode=ro`:

| Owner | Saved activities | Latest saved start (UTC) | Latest create webhook received (UTC) | Latest update received (UTC) |
|---|---:|---|---|---|
| 2025855 | 139 | 2026-11-08 14:50:11 | 2026-10-14 13:29:12 | 2026-11-08 17:56:57 |
| 13344161 | 618 | 2026-10-15 13:40:06 | 2026-10-15 14:50:10 | 2026-10-15 11:14:21 |

Owner 2025855 has legacy empty saved scopes; its saved access-token expiry was 2026-11-08 22:05:11 UTC (refresh may still work). Owner 13344161 has activity read/read_all/write and read scopes, expiry 2026-11-09 15:01:39 UTC. No credentials or raw activity/webhook payloads were printed. These observations don't establish why a particular activity's webhook is missing; no missing-activity ID was supplied.

## Behavior

- `POST /api/sync/latest` authenticates the owner from existing web cookie or the app's signed bearer session, never a query/body user ID. Cookie POSTs require the existing same-origin check. GET is not a mutation route.
- Atomic per-owner enqueue coalesces queued/running/retry/waiting/blocked discovery and imposes a 60-second cooldown after the latest job update. Admin Fetch latest reuses this path.
- Each worker claim makes **one list call, 50 summaries max**, with a 30-second deadline. The page and fixed `after`/`before` range are durable. Child processing jobs and page advancement commit together. A short page ends discovery; a full page schedules the next claim after two seconds. No arbitrary cap silently discards subsequent pages.
- The lower watermark is the previous completed discovery's fixed upper boundary, with a one-second overlap. Legacy jobs without that boundary do not count. For the first scan, use the oldest saved activity (not the newest: a webhook may already have saved it while leaving older gaps). With no saved activities, bootstrap the latest seven days; historical import remains separate. Known activity IDs/deletion tombstones are skipped. Existing job dedupe and ingestion handle missing IDs idempotently.
- This is start-date-window reconciliation, not an exhaustive historical audit: newly uploaded/backdated activities older than the committed watermark still rely on webhooks/history import. A fixed upper bound prevents ordinary newly started activities from shifting subsequent pages; Strava does not provide snapshot pagination for concurrent deletion/backdated uploads.
- Reuses factory quota transport and runner retry handling: shared persisted short/daily cooldowns, Retry-After, rate-limit waits without attempt consumption, auth/permission blocks with reconnect instructions, regular bounded transient retries. Discovered child jobs retain existing backfill quota headroom.
- Web has an explicit **Sync with Strava** button; POST renders queued/waiting status then existing GET polling updates progress and exposes a reload link for new analysis. GET polling never enqueues. Native Activities pull-to-refresh and button request sync, reload the feed, and poll saved feed/status every five seconds for up to a minute. Profile offers the same explicit action. Automatic startup remains read-only. Sign-out cancels native polling and invalidates in-flight replies.
- Optional map/publishing outcomes remain separate. Old map errors no longer obscure active discovery status. Sync never invokes retry-failed-work or retries the seven existing map jobs.

## Verified

- `go test ./...` — pass.
- `go vet ./...` — pass.
- `go test -race ./internal/storage ./internal/jobs ./internal/web` — pass (rerun after final edits as noted in task report).
- `node --test internal/web/static/sync-status.test.cjs` — 2 passed.
- `xcodebuild test -quiet -project ios/WeirdStats.xcodeproj -scheme WeirdStats -destination 'platform=iOS Simulator,id=E3277EBA-C768-4357-ACC0-CFAB3EF88518' -only-testing:WeirdStatsTests CODE_SIGNING_ALLOWED=NO` — pass.
- `git diff --check` — pass.

Regression tests cover 118 missing IDs across three durable pages even with newest/oldest already saved, real ingestion of three discovered activities via local HTTP fixtures, duplicate page/request coalescing, concurrent owner single-flight/cooldown, owner/CSRF/app-session auth, daily 429 scheduling, preserved retry cursor and reconnect blocking, native POST-before-reload, GET-only polling, and active discovery alongside optional failures. The discovery, UI trigger, and map-status precedence regressions were observed failing before implementation. No tests use production tokens or call Strava.

## Manual deployment and acceptance (not executed)

1. Review the existing uncommitted map-status/icon work and this sync diff together. No new schema migration is required. Back up the production SQLite database consistently using the existing operational backup process.
2. Build/publish a reviewed backend image, then use the service's normal Tinyserve image-update/recreate workflow for **weirdstats only**. The currently running service is `tinyserve-weirdstats-1`, image `ghcr.io/ptmt/weirdstats:latest`, database mount `/data`. Do not edit generated staging Compose files or restart unrelated services.
3. Verify startup, callback subscription log and read-only authenticated GET status. Confirm the new POST route exists (anonymous request must be 401), and web assets include Sync with Strava.
4. Install/test the matching native build. Sign in with an app session. Tap Sync with Strava once. Confirm one owner `sync_latest` job; subsequent immediate taps reuse it. Observe list discovery pages and process jobs, then confirm missing feed entries appear. Respect waiting/daily-reset status; reconnect if blocked. Do not use Retry failed work as an acceptance shortcut.
5. Check discovery counters/cursor and final feed dates read-only. Optional map failures may remain and do not mean import failed. No promises of restored live webhook delivery or maps are made by these tests.

No production writes, job retries, deploy, commit or push were performed. Pre-existing icon/design changes were not touched.
