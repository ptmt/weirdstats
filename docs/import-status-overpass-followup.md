# Import status and Overpass follow-up

> Historical investigation: the later explicit discovery repair and deployment instructions are in [explicit-strava-sync.md](explicit-strava-sync.md). The GET-only reload observations below describe the original behavior; Activities now also offers a distinct Sync with Strava POST action.

## Corrected behavior

`failed`, `blocked`, and `waiting` now describe import/discovery work only. Optional map and publishing failures/blocks have separate JSON counts (`map_failed`, `map_blocked`, `publishing_failed`, `publishing_blocked`). Existing completed analysis is still counted from saved activity statistics. Current-stage deduplication and owner scoping are unchanged. The regression fixture uses the seven incident IDs with synthetic activities, not production data: seven map errors produce zero import failures.

Web (initial template and subsequent status polling) and native Profile show optional map/publishing failures separately, retain safe grouped diagnostic references and retry/reconnect controls, and no longer describe enrichment failures as failed activities. Native decoding accepts older servers without the added fields. Deploy the backend change as well as updating native UI: old servers still send the old aggregate count.

Enrichment retry logs now preserve an allowlisted cause and numeric HTTP status. They never interpolate the original error, URL, coordinates, credentials, or provider body. HTTP errors from Overpass carry only a typed status, not provider body text. Existing sanitized user/job messages remain unchanged.

## Refresh investigation — not a repaired ingestion outage

Native `AppModel.refresh()` sends GETs to `/api/mobile/me` and `/api/mobile/activities`; `MobileActivities` reads stored activities/routes/facts and sync status. It does **not** request a new Strava fetch. The web reload link likewise navigates to stored activities. Status polling GETs `/api/sync`; it does not enqueue discovery. UI text now explicitly calls this reloading imported activities and says it does not fetch new Strava activities. No new refetch loop, background discovery, retry action, or production request was introduced.

The existing separate admin “Fetch latest” action creates `sync_latest`; `Ingestor.SyncLatestActivity` lists only the single newest Strava activity, fetches it, and enqueues analysis. It is not catch-up for every missed activity. The cause of missing *new* activities remains unproven: inspect webhook delivery/subscription, connection/permissions, the expected missing activity/time, and discovery history before changing ingestion. Fixing the seven optional errors cannot by itself import a missing activity.

## Endpoint configuration and bounded fallback

Existing `OVERPASS_URLS` is an ordered comma-separated endpoint list and takes precedence over `OVERPASS_URL`. The client already rotated this list, but connection-refused was not retryable; 429 was, amplifying rate limits. The fix permits fallback only for classified transport failures and HTTP 5xx. HTTP 429 stops immediately without contacting a mirror; ordinary 4xx, invalid JSON, and cancellation do not trigger fallback. Existing five-attempt default and 15-second overall query deadline remain; explicit MaxAttempts and context limits are respected. Consent/access checks still execute before each attempt. Endpoint configuration/defaults were not changed.

One identical tiny nonpersonal query (`[out:json][timeout:5];node(1);out ids;`) was sent at most once per candidate from the development Mac, never with activity GPS and never against the seven jobs:

- `https://overpass.kumi.systems/api/interpreter`: timed out after 12 seconds.
- `https://overpass.private.coffee/api/interpreter`: timed out after 12 seconds.
- `https://overpass.osm.ch/api/interpreter`: returned valid Overpass JSON with an empty elements array. This is a regional Swiss instance, **not verified as suitable for global activity coverage**; no default or production configuration was switched to it.
- `https://overpass-api.nextzen.org/api/interpreter`: DNS resolution failed.
- `https://overpass.osm.jp/api/interpreter`: connection failed.

Development-host reachability does not establish production-container reachability. No globally suitable live alternate was verified; the configurable fallback path is exercised with local HTTP servers, including connection-refused → success and 503 → success. Production endpoint selection remains an operational check, not a claim of restored maps.

## Verification and scope

Tests were added first and observed failing for the incorrect seven-failure aggregation, misleading status/template, missing native fields, missing cause log, refusal to fall back on connection-refused, and five requests on a 429. Verified with `go test ./...`, `node --test internal/web/static/sync-status.test.cjs`, and simulator `xcodebuild test ... -only-testing:WeirdStatsTests CODE_SIGNING_ALLOWED=NO`. The refresh regression asserts exactly the two read-only native endpoints. No production jobs, databases, deployments, retries, commits, or pushes were changed. Pre-existing icon/design changes were left untouched.
