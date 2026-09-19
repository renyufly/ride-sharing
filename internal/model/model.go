// Package model defines values shared by generators, matchers, pipelines, and
// reports. It intentionally contains no matching behavior.
package model

type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

type Point2D struct {
	X float64 // meters
	Y float64 // meters
}

type Rider struct {
	UID      uint64
	Location GeoPoint
	Point    Point2D
}

// Order keeps the business identifier separate from the contiguous sequence
// used to preserve arrival order inside one benchmark run.
type Order struct {
	ID               uint64
	Sequence         uint64
	PlannedArrivalNs int64
	Pickup           GeoPoint
	Point            Point2D
}

// Assignment is the result of matching one order. The squared distance is
// retained because nearest-neighbor comparison does not require a square root.
type Assignment struct {
	OrderID               uint64
	Sequence              uint64
	RiderUID              uint64
	DistanceSquaredMeters float64
}
