# Traffic Light Detection From Strava GPS

- Goal: detect stop events in GPS tracks, map-match them, and label which stops align with traffic lights vs. other causes (turns, congestion, rests).

## Stop Extraction
- Resample streams to ~1 Hz if gaps exist; drop obvious outliers (e.g., jumps >50 m between samples).
- Compute speed between points via haversine distance / delta time; smooth with a short moving median or low-pass filter to tame GPS noise.
- Mark stop intervals where smoothed speed < 0.5–1.0 m/s for at least 3–8 s; merge adjacent stops within ~10–20 m or short time gaps.
- Ignore very long stops (>90–120 s) as likely rests; take the stop point as the centroid of the merged interval.
- Filter stops immediately after sharp heading changes (e.g., >60–90 degrees) to reduce false positives from turns/U-turns/driveways.

## Map Matching
- Snap the track to road geometry to reduce drift and get segment context.
- Services: Mapbox Map Matching API, Google Roads API, HERE Map Matching, TomTom Map Matching, or self-hosted OSRM/Valhalla.
- Use matched geometry to project stop points and measure distance to intersections/nodes.

## Traffic-Signal POI Sources
- OpenStreetMap via Overpass (`highway=traffic_signals`) or OSM-derived tilesets.
- Mapbox vector tiles/tilesets (OSM-based); HERE Places/traffic lights; TomTom traffic/POI layers; Google Places (no direct signal POI, but intersections can help heuristics).

## Disambiguation Heuristics
- Signals are likely near intersection nodes; prefer stops near road junctions and on arterials.
- Down-rank stops occurring immediately after sharp turns.
- Congestion vs. signals: cluster stops along the same segment—mid-block clusters suggest traffic, not lights.
- Repeatability: stops recurring across multiple rides at the same spot are likely real lights.

## Suggested Workflow
1) Load activity polyline and streams; resample/clean.
2) Smooth speed; detect/merge stop intervals; compute stop centroids.
3) Map-match the track; project stops onto matched geometry.
4) Fetch nearby traffic-signal POIs; join stops to nearest signal within ~15–30 m along the matched road.
5) Label matched stops as likely lights; keep unmatched stops for congestion/other analysis.

## Go Server/API Dependencies & Config Needs
- HTTP client with timeout, retries, and rate limiting per provider; use context-aware requests.
- Geometry helpers (haversine, heading, clustering); can be standard-library math or a small geo lib if desired.
- Caching layer for POIs/map-matching responses (SQLite table or in-memory LRU) to avoid repeat calls.
- Config/env vars for API access:
  - `MAPBOX_TOKEN` for Map Matching/tiles.
  - `GOOGLE_MAPS_KEY` for Roads/Places.
  - `HERE_API_KEY` (or app id/code) for HERE map matching/places.
  - `TOMTOM_KEY` for TomTom map matching/traffic POIs.
  - `OVERPASS_URL` for a chosen Overpass instance; `OSRM_BASE_URL` or `VALHALLA_BASE_URL` if self-hosting.
- Optional background job to prefetch/cache traffic-signal POIs for recent ride regions to reduce latency.

## Licensing/Usage Notes (high level, verify per provider)
- OpenStreetMap/Overpass: data under ODbL; requires attribution; distributing derived databases may trigger share-alike obligations.
- Mapbox: commercial terms; requires token; derived data usage limited to Mapbox TOS; attribution required when displaying Mapbox data.
- Google Roads/Places: billing-enabled key; strict terms on storage/caching and derivative works; attribution required.
- HERE: commercial terms; API key/app credentials; usage caps and attribution per HERE terms.
- TomTom: commercial terms; API key; usage caps; attribution required.

## Coffee Stop Detection (Cafés/Restaurants)
- Strategy mirrors traffic-light workflow: detect stop intervals, then classify by nearby POIs instead of signals.
- POI sources: Overpass (e.g., `amenity=cafe|restaurant|fast_food|bar`, `shop=convenience|supermarket`), Mapbox/HERE/TomTom places, Google Places (Food/Drink categories).
- Heuristics:
  - Require longer dwell times (e.g., >4–5 min) to distinguish from lights; cap ultra-long breaks as “rest”.
  - Look for stops near POIs within ~30–50 m; prefer locations on the same side of the road or with pedestrian access paths if available.
  - Cluster POIs: multiple food/coffee POIs within a small radius increase confidence.
  - Repeat visits on different rides to the same spot also raise confidence.
  - Exclude stops adjacent to intersections with traffic signals unless dwell time is well above light durations.
- Implementation notes:
  - Reuse stop extraction and map matching.
  - Add POI-category filtering and a classification flag per stop (`traffic_light`, `coffee`, `other`), optionally with a confidence score.
  - Cache POI lookups per tile/bbox to avoid repeat queries; respect provider caching/retention rules (Google limits caching).
