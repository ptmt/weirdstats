package maps

type FeatureType string

const (
	FeatureTrafficLight FeatureType = "traffic_light"
	FeatureRoadCrossing FeatureType = "road_crossing"
	FeatureCafe         FeatureType = "cafe"
	FeatureRestaurant   FeatureType = "restaurant"
	FeatureFastFood     FeatureType = "fast_food"
	FeatureBar          FeatureType = "bar"
)

type Feature struct {
	Type FeatureType
	Name string
}

type POI struct {
	Feature
	Lat  float64
	Lon  float64
	Tags map[string]string
}

type BBox struct {
	South float64
	West  float64
	North float64
	East  float64
}

// Road represents a road segment from OSM with its geometry.
type Road struct {
	ID       int64
	Name     string
	Highway  string // road type: primary, secondary, tertiary, residential, etc.
	Geometry []LatLon
}

type LatLon struct {
	Lat float64
	Lon float64
}

type API interface {
	NearbyFeatures(lat, lon float64) ([]Feature, error)
}
