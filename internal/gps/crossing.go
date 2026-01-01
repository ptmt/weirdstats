package gps

import (
	"math"

	"weirdstats/internal/maps"
)

// CrossingResult describes a detected road crossing after a stop.
type CrossingResult struct {
	Crossed  bool
	RoadName string
	RoadType string
}

// DetectRoadCrossing checks if the path after a stop crosses a road.
// It looks at GPS points from stopEndIdx onwards and checks if the path
// intersects any of the provided roads.
func DetectRoadCrossing(points []Point, stopEndIdx int, roads []maps.Road) CrossingResult {
	if stopEndIdx < 0 || stopEndIdx >= len(points)-1 || len(roads) == 0 {
		return CrossingResult{}
	}

	// Look at the path segment from stop end to ~30m or 10 points after
	startPt := points[stopEndIdx]
	endIdx := stopEndIdx + 1

	// Find a point that's at least 15m away or up to 10 points ahead
	for i := stopEndIdx + 1; i < len(points) && i < stopEndIdx+15; i++ {
		dist := haversineMeters(startPt.Lat, startPt.Lon, points[i].Lat, points[i].Lon)
		if dist >= 15 {
			endIdx = i
			break
		}
		endIdx = i
	}

	endPt := points[endIdx]
	pathSeg := segment{
		x1: startPt.Lon, y1: startPt.Lat,
		x2: endPt.Lon, y2: endPt.Lat,
	}

	// Check each road for intersection
	for _, road := range roads {
		for i := 0; i < len(road.Geometry)-1; i++ {
			roadSeg := segment{
				x1: road.Geometry[i].Lon, y1: road.Geometry[i].Lat,
				x2: road.Geometry[i+1].Lon, y2: road.Geometry[i+1].Lat,
			}
			if segmentsIntersect(pathSeg, roadSeg) {
				return CrossingResult{
					Crossed:  true,
					RoadName: road.Name,
					RoadType: road.Highway,
				}
			}
		}
	}

	return CrossingResult{}
}

// FindStopEndIndex finds the index of the first point after the stop ends
// (first point with speed above threshold after the stop started).
func FindStopEndIndex(points []Point, stopStartTime float64, threshold float64, activityStart float64) int {
	inStop := false
	for i, p := range points {
		elapsed := p.Time.Sub(points[0].Time).Seconds()
		if !inStop && elapsed >= stopStartTime {
			inStop = true
		}
		if inStop && p.Speed > threshold {
			return i
		}
	}
	return -1
}

type segment struct {
	x1, y1, x2, y2 float64
}

// segmentsIntersect checks if two line segments intersect.
func segmentsIntersect(a, b segment) bool {
	d1 := direction(b.x1, b.y1, b.x2, b.y2, a.x1, a.y1)
	d2 := direction(b.x1, b.y1, b.x2, b.y2, a.x2, a.y2)
	d3 := direction(a.x1, a.y1, a.x2, a.y2, b.x1, b.y1)
	d4 := direction(a.x1, a.y1, a.x2, a.y2, b.x2, b.y2)

	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}

	// Check collinear cases
	if d1 == 0 && onSegment(b.x1, b.y1, b.x2, b.y2, a.x1, a.y1) {
		return true
	}
	if d2 == 0 && onSegment(b.x1, b.y1, b.x2, b.y2, a.x2, a.y2) {
		return true
	}
	if d3 == 0 && onSegment(a.x1, a.y1, a.x2, a.y2, b.x1, b.y1) {
		return true
	}
	if d4 == 0 && onSegment(a.x1, a.y1, a.x2, a.y2, b.x2, b.y2) {
		return true
	}

	return false
}

// direction returns the cross product of vectors (p2-p1) and (p3-p1).
func direction(x1, y1, x2, y2, x3, y3 float64) float64 {
	return (x3-x1)*(y2-y1) - (y3-y1)*(x2-x1)
}

// onSegment checks if point (px, py) lies on segment (x1,y1)-(x2,y2).
func onSegment(x1, y1, x2, y2, px, py float64) bool {
	return px >= math.Min(x1, x2) && px <= math.Max(x1, x2) &&
		py >= math.Min(y1, y2) && py <= math.Max(y1, y2)
}

// haversineMeters calculates the distance between two points in meters.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000 // meters
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}
