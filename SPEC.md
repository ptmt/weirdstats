Weirdstats is a project that listens to Strava webhooks, and update activity description sometimes or mute (hide) them

It has UI frontend (landing page, settings page, auth).
It has incoming webhook processor that accepts Strava API call and record it in the sql.
It has a worker that process queue from sql and mark it as processed.

## Data model

You have users, their settings, they authorization information, their activities of different types. Each activity (GPS info + metadata) parsed and stored only information we need — meta information + statistics we calculate. Later when we add a new "fact/stat extraction" we may reparse all activities.

## Features

- Allow users setup muting activies based on conditions (e.g. hide my virual ride if it's less than 1 hour)
- Show additional stats in activity description. Start with this one: show how many stops and for how long in total during the ride activity contains. The second one - how many light traffic stops. For that you need some Maps API that will obrain what are located near the coordinates of stops (pauses) in gps activity.
- Instagram Export. Export polyline with some random stats with stunnging typography and configurable metadata as png. 

## Rule engine design

Goal: a structured, extendable rules system that is used both for hiding activities and for other stats-driven behaviors. Rules evaluate against a shared set of metrics with display metadata (name, unit) so UI and engine stay aligned.

### Core concepts

- Metric: a named, typed value computed from an activity and its derived stats.
- Unit: display unit for metrics (e.g. "km", "min").
- Condition: (metric, operator, value[s]) triple.
- Rule: named set of conditions + enabled flag. Default evaluation is "match all conditions".
- Outcome: for now only "hide activity", but extendable to other actions.

### Metric registry

Metrics are defined in a central registry with:
- ID (enum-like string): e.g. "distance_m", "duration_s", "moving_time_s", "activity_type".
- Display name: e.g. "Total distance", "Total time", "Moving time", "Activity type".
- Unit: e.g. "m", "s", "s", "".
- Value type: number or enum.
- Resolver function: computes the metric from stored activity + stats + derived data.

Initial metrics:
- distance_m (number, unit "m"): total distance in meters.
- duration_s (number, unit "s"): total elapsed activity time.
- moving_time_s (number, unit "s"): total moving time (requires streams or Strava summary).
- activity_type (enum): Strava activity type (Ride, Run, etc).

Notes:
- UI can map meters/seconds into km/min for display, but storage and evaluation stay in base units.
- Metrics can be reused by other features (stats summaries, UI badges, exports).

### Operators

Operators depend on value type:
- number: eq, neq, lt, lte, gt, gte, between.
- enum: eq, neq, in, not_in.

### Rule schema (storage)

Store rules as JSON to avoid schema churn:

Rule
- id
- user_id
- name
- enabled
- match: "all" or "any"
- conditions: []Condition
- created_at, updated_at

Condition
- metric_id
- operator
- values (array; 1 or 2 items depending on operator)

Values are stored in base units for numeric metrics.

### Evaluation pipeline

1) Build a metric registry at startup.
2) On activity processing, compute needed metrics lazily:
   - Collect all metric IDs referenced by enabled rules.
   - Resolve each metric once per activity.
3) Evaluate each rule:
   - For each condition, compare metric value vs operator values.
   - Apply match strategy ("all" / "any").
4) If rule matches, emit outcome (initially "hide activity").

### Validation

When saving or updating rules:
- Ensure metric_id exists in registry.
- Ensure operator is allowed for metric type.
- Ensure values count/type is correct.
- Normalize numeric values to base units.

### UI model

UI queries the registry for:
- Metric display name and unit.
- Operator choices per metric type.
- Placeholder / helper text.

### Integration points

- Activity ingestion or worker pipeline should run rule evaluation after stats are computed.
- Store evaluation result on activity (e.g. activities.hidden boolean) and optionally update description.
- For reprocessing, allow batch reevaluation when rules change.

### Incremental rollout

1) Add registry + evaluator with a small set of metrics.
2) Update settings UI to create JSON-based rules.
3) Apply rules during processing and add "hidden" field to activities.
4) Add backfill job to reevaluate existing activities on rule change.

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
