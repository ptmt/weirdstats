package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DefaultOverpassURL = "https://overpass-api.de/api/interpreter"
const defaultCacheTTL = 24 * time.Hour

type OverpassClient struct {
	BaseURL      string
	HTTPClient   *http.Client
	Timeout      time.Duration
	CacheTTL     time.Duration
	DisableCache bool
	MaxAttempts  int
	BackoffBase  time.Duration
	MirrorURLs   []string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

func (c *OverpassClient) NearbyFeatures(lat, lon float64) ([]Feature, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.effectiveTimeout())
	defer cancel()

	query := fmt.Sprintf(`[out:json][timeout:25];
(
  node(around:40,%.6f,%.6f)["highway"="traffic_signals"];
);
out body;`, lat, lon)

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return nil, err
	}

	var features []Feature
	for _, el := range elements {
		if el.Tags["highway"] == "traffic_signals" {
			name := el.Tags["name"]
			features = append(features, Feature{Type: FeatureTrafficLight, Name: name})
		}
	}
	return features, nil
}

func (c *OverpassClient) FetchPOIs(ctx context.Context, bbox BBox, includeTrafficLights bool, includeFood bool) ([]POI, error) {
	if !includeTrafficLights && !includeFood {
		return nil, errors.New("no feature types requested")
	}

	var queries []string
	if includeTrafficLights {
		queries = append(queries, fmt.Sprintf(`node["highway"="traffic_signals"](%s);`, bbox.String()))
	}
	if includeFood {
		queries = append(queries, fmt.Sprintf(`nwr["amenity"~"^(cafe|restaurant|fast_food|bar)$"](%s);`, bbox.String()))
	}

	query := fmt.Sprintf(`[out:json][timeout:25];
(
%s
);
out center;`, strings.Join(queries, "\n"))

	ctx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return nil, err
	}

	return poisFromOverpassElements(elements), nil
}

func (c *OverpassClient) FetchNearbyFoodPOIs(ctx context.Context, lat, lon float64, radiusMeters int) ([]POI, error) {
	if radiusMeters <= 0 {
		radiusMeters = 40
	}

	query := fmt.Sprintf(`[out:json][timeout:25];
(
  nwr(around:%d,%.6f,%.6f)["amenity"~"^(cafe|restaurant)$"];
);
out center;`, radiusMeters, lat, lon)

	ctx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return nil, err
	}

	return poisFromOverpassElements(elements), nil
}

func (c *OverpassClient) FetchLandmarkPOIs(ctx context.Context, bbox BBox) ([]POI, error) {
	query := fmt.Sprintf(`[out:json][timeout:25];
(
  nwr["name"]["tourism"~"^(attraction|artwork|museum|viewpoint)$"](%s);
  nwr["name"]["historic"~"^(monument|memorial|castle|ruins|archaeological_site)$"](%s);
  nwr["name"]["amenity"="place_of_worship"](%s);
  nwr["name"]["building"~"^(church|cathedral)$"](%s);
);
out center;`, bbox.String(), bbox.String(), bbox.String(), bbox.String())

	ctx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return nil, err
	}

	return poisFromOverpassElements(elements), nil
}

func (c *OverpassClient) FetchMapContext(ctx context.Context, bbox BBox) (MapContext, error) {
	query := fmt.Sprintf(`[out:json][timeout:25];
(
  way["highway"~"^(motorway|trunk|primary|secondary|tertiary|motorway_link|trunk_link|primary_link|secondary_link|tertiary_link)$"](%s);
  way["waterway"~"^(river|canal|stream)$"](%s);
  way["natural"="water"](%s);
  way["waterway"="riverbank"](%s);
  way["landuse"="reservoir"](%s);
  nwr["natural"~"^(peak|volcano)$"]["name"](%s);
);
out geom center;`, bbox.String(), bbox.String(), bbox.String(), bbox.String(), bbox.String(), bbox.String())

	ctx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return MapContext{}, err
	}

	return mapContextFromOverpassElements(elements), nil
}

// FetchNearbyRoads returns roads within the given radius (meters) of a point.
// Only fetches roads suitable for crossing detection (excludes footways, paths, etc.)
func (c *OverpassClient) FetchNearbyRoads(ctx context.Context, lat, lon float64, radiusMeters int) ([]Road, error) {
	if radiusMeters <= 0 {
		radiusMeters = 30
	}

	// Query for ways with highway tag that are actual roads (not footways/paths)
	query := fmt.Sprintf(`[out:json][timeout:25];
way(around:%d,%.6f,%.6f)["highway"~"^(primary|secondary|tertiary|unclassified|residential|living_street|service|trunk|primary_link|secondary_link|tertiary_link)$"];
out geom;`, radiusMeters, lat, lon)

	ctx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	elements, err := c.fetchWithCache(ctx, query)
	if err != nil {
		return nil, err
	}

	var roads []Road
	for _, el := range elements {
		if el.Type != "way" || len(el.Geometry) < 2 {
			continue
		}
		geom := make([]LatLon, len(el.Geometry))
		for i, pt := range el.Geometry {
			geom[i] = LatLon{Lat: pt.Lat, Lon: pt.Lon}
		}
		roads = append(roads, Road{
			ID:       el.ID,
			Name:     el.Tags["name"],
			Highway:  el.Tags["highway"],
			Geometry: geom,
		})
	}
	return roads, nil
}

func (c *OverpassClient) fetchWithCache(ctx context.Context, query string) ([]overpassElement, error) {
	if ttl := c.effectiveCacheTTL(); ttl > 0 {
		if cached, ok := c.getCached(query); ok {
			return cached, nil
		}
	}
	elements, err := c.runQueryWithRetry(ctx, query)
	if err != nil {
		return nil, err
	}
	if ttl := c.effectiveCacheTTL(); ttl > 0 {
		c.setCached(query, elements, ttl)
	}
	return elements, nil
}

func (c *OverpassClient) runQueryWithRetry(ctx context.Context, query string) ([]overpassElement, error) {
	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	baseSleep := c.BackoffBase
	if baseSleep <= 0 {
		baseSleep = time.Second
	}
	endpoints := c.baseURLs()
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		base := endpoints[attempt%len(endpoints)]
		elements, status, err := c.runQueryOnce(ctx, base, query)
		if err == nil {
			return elements, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !isRetryable(status, err) || attempt == maxAttempts-1 {
			break
		}
		sleep := baseSleep << attempt
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
	}
	return nil, lastErr
}

func (c *OverpassClient) runQueryOnce(ctx context.Context, base string, query string) ([]overpassElement, int, error) {
	endpoint, err := url.Parse(base)
	if err != nil {
		return nil, 0, fmt.Errorf("parse overpass url: %w", err)
	}
	params := url.Values{}
	params.Set("data", query)
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, resp.StatusCode, fmt.Errorf("overpass status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded overpassResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, resp.StatusCode, err
	}

	return decoded.Elements, resp.StatusCode, nil
}

func (c *OverpassClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: c.effectiveTimeout()}
}

func (c *OverpassClient) effectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 15 * time.Second
}

func (c *OverpassClient) effectiveCacheTTL() time.Duration {
	if c.DisableCache {
		return 0
	}
	if c.CacheTTL > 0 {
		return c.CacheTTL
	}
	return defaultCacheTTL
}

func (c *OverpassClient) getCached(key string) ([]overpassElement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		return nil, false
	}
	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.elements, true
}

func (c *OverpassClient) setCached(key string, elements []overpassElement, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = make(map[string]cacheEntry)
	}
	c.cache[key] = cacheEntry{
		elements:  elements,
		expiresAt: time.Now().Add(ttl),
	}
}

func (b BBox) String() string {
	return fmt.Sprintf("%f,%f,%f,%f", b.South, b.West, b.North, b.East)
}

func (c *OverpassClient) baseURLs() []string {
	if len(c.MirrorURLs) > 0 {
		return c.MirrorURLs
	}
	if c.BaseURL != "" {
		return []string{c.BaseURL}
	}
	return []string{DefaultOverpassURL}
}

func classifyPOI(tags map[string]string) FeatureType {
	switch tags["amenity"] {
	case "cafe":
		return FeatureCafe
	case "restaurant":
		return FeatureRestaurant
	case "fast_food":
		return FeatureFastFood
	case "bar":
		return FeatureBar
	case "place_of_worship":
		return FeatureType("place_of_worship")
	}
	if tourism := tags["tourism"]; tourism != "" {
		return FeatureType(tourism)
	}
	if historic := tags["historic"]; historic != "" {
		return FeatureType(historic)
	}
	if tags["highway"] == "traffic_signals" {
		return FeatureTrafficLight
	}
	if building := tags["building"]; building != "" {
		return FeatureType(building)
	}
	return FeatureType(tags["amenity"])
}

func poisFromOverpassElements(elements []overpassElement) []POI {
	pois := make([]POI, 0, len(elements))
	for _, el := range elements {
		poiType := classifyPOI(el.Tags)
		lat, lon := el.coordinates()
		pois = append(pois, POI{
			Feature: Feature{
				Type: poiType,
				Name: el.Tags["name"],
			},
			Lat:  lat,
			Lon:  lon,
			Tags: el.Tags,
		})
	}
	return pois
}

func mapContextFromOverpassElements(elements []overpassElement) MapContext {
	ctx := MapContext{
		Roads:     make([]Road, 0),
		Waterways: make([]PolylineFeature, 0),
		Waters:    make([]PolygonFeature, 0),
		Peaks:     make([]POI, 0),
	}
	for _, el := range elements {
		switch {
		case isMajorRoad(el.Tags) && len(el.Geometry) >= 2:
			ctx.Roads = append(ctx.Roads, Road{
				ID:       el.ID,
				Name:     el.Tags["name"],
				Highway:  el.Tags["highway"],
				Geometry: latLonGeometry(el.Geometry),
			})
		case isWaterway(el.Tags) && len(el.Geometry) >= 2:
			ctx.Waterways = append(ctx.Waterways, PolylineFeature{
				Name:     el.Tags["name"],
				Kind:     el.Tags["waterway"],
				Geometry: latLonGeometry(el.Geometry),
			})
		case isWaterArea(el.Tags) && len(el.Geometry) >= 3:
			ctx.Waters = append(ctx.Waters, PolygonFeature{
				Name:     el.Tags["name"],
				Kind:     waterAreaKind(el.Tags),
				Geometry: latLonGeometry(el.Geometry),
			})
		case isPeak(el.Tags):
			lat, lon := el.coordinates()
			ctx.Peaks = append(ctx.Peaks, POI{
				Feature: Feature{
					Type: FeatureType(el.Tags["natural"]),
					Name: el.Tags["name"],
				},
				Lat:  lat,
				Lon:  lon,
				Tags: el.Tags,
			})
		}
	}
	return ctx
}

func latLonGeometry(points []overpassLatLon) []LatLon {
	geom := make([]LatLon, 0, len(points))
	for _, pt := range points {
		geom = append(geom, LatLon{Lat: pt.Lat, Lon: pt.Lon})
	}
	return geom
}

func isMajorRoad(tags map[string]string) bool {
	switch tags["highway"] {
	case "motorway", "trunk", "primary", "secondary", "tertiary", "motorway_link", "trunk_link", "primary_link", "secondary_link", "tertiary_link":
		return true
	default:
		return false
	}
}

func isWaterway(tags map[string]string) bool {
	switch tags["waterway"] {
	case "river", "canal", "stream":
		return true
	default:
		return false
	}
}

func isWaterArea(tags map[string]string) bool {
	return tags["natural"] == "water" || tags["waterway"] == "riverbank" || tags["landuse"] == "reservoir"
}

func waterAreaKind(tags map[string]string) string {
	if tags["natural"] == "water" {
		return "water"
	}
	if tags["waterway"] == "riverbank" {
		return "riverbank"
	}
	if tags["landuse"] == "reservoir" {
		return "reservoir"
	}
	return ""
}

func isPeak(tags map[string]string) bool {
	switch tags["natural"] {
	case "peak", "volcano":
		return strings.TrimSpace(tags["name"]) != ""
	default:
		return false
	}
}

type overpassElement struct {
	Type     string            `json:"type"`
	ID       int64             `json:"id"`
	Lat      float64           `json:"lat"`
	Lon      float64           `json:"lon"`
	Center   *overpassLatLon   `json:"center,omitempty"`
	Tags     map[string]string `json:"tags"`
	Geometry []overpassLatLon  `json:"geometry,omitempty"` // for ways with out geom
}

func (e overpassElement) coordinates() (float64, float64) {
	if e.Center != nil {
		return e.Center.Lat, e.Center.Lon
	}
	return e.Lat, e.Lon
}

type overpassLatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

type cacheEntry struct {
	elements  []overpassElement
	expiresAt time.Time
}

func isRetryable(status int, err error) bool {
	if status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		return true
	}
	var netErr interface{ Temporary() bool }
	if errors.As(err, &netErr) && netErr.Temporary() {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}
