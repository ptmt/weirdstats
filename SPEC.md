Weirdstats is a project that listens to Strava webhooks, and update activity description sometimes or mute (hide) them

It has UI frontend (landing page, settings page, auth).
It has incoming webhook processor that accepts Strava API call and record it in the sql.
It has a worker that process queue from sql and mark it as processed.

## Data model

You have users, their settings, they authorization information, their activities of different types. Each activity (GPS info + metadata) parsed and stored only information we need — meta information + statistics we calculate. Later when we add a new "fact/stat extraction" we may reparse all activities.

## Features

- Allow users setup muting activies based on conditions (e.g. hide my virual ride if it's less than 1 hour)
- Show additional stats in activity description. Start with this one: show how many stops and for how long in total during the ride activity contains. The second one - how many light traffic stops. For that you need some Maps API that will obrain what are located near the coordinates of stops (pauses) in gps activity.

## Tech stack

- Go language, for memory footprint and a single binary experience
- SQLite for database
- Focus on redability and maintability of the code. Should be able to fix 5 years later, so composibility matters.
- Unit tests for everything for fast feedback loop, allow saving some gps activities to repository to use them as data in tests.
- Think of really useful integration tests, fewer but quality ones and that you as AI can consume.
- Prepare deployment guide for Hetzner VPS

## Strava API

Webhooks https://developers.strava.com/docs/webhooks/

Strava API e.g. get activity https://developers.strava.com/docs/reference/#api-Activities-getActivityById 

### Webhook verification and signing

- Verification uses GET `/webhook?hub.challenge=...&hub.verify_token=...` and must echo the challenge.
- Incoming POST `/webhook` validates `X-Strava-Signature` when `STRAVA_WEBHOOK_SECRET` is set.

### OAuth tokens

- Access tokens expire quickly; use refresh tokens via the OAuth refresh flow.
- Store `access_token`, `refresh_token`, `expires_at` in SQLite and refresh when expired.

### Activity ingestion

- Fetch activity metadata plus streams (`latlng`, `time`, `velocity_smooth`) before computing stop stats.
