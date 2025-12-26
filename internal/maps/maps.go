package maps

type FeatureType string

const FeatureTrafficLight FeatureType = "traffic_light"

type Feature struct {
	Type FeatureType
	Name string
}

type API interface {
	NearbyFeatures(lat, lon float64) ([]Feature, error)
}
