// Package geo validates geographic coordinates and projects them into the
// matcher's local, meter-based coordinate system.
package geo

import (
	"errors"
	"fmt"
	"math"

	"ride-sharing/internal/model"
)

const EarthRadiusMeters = 6_371_008.8

// BoundingBox is an inclusive latitude/longitude rectangle.
type BoundingBox struct {
	MinLatitude  float64
	MaxLatitude  float64
	MinLongitude float64
	MaxLongitude float64
}

// SanFranciscoBounds contains the existing demo routes and provides enough
// surrounding area for deterministic benchmark data.
var SanFranciscoBounds = BoundingBox{
	MinLatitude:  37.70,
	MaxLatitude:  37.82,
	MinLongitude: -122.52,
	MaxLongitude: -122.35,
}

func ValidatePoint(point model.GeoPoint) error {
	if math.IsNaN(point.Latitude) || math.IsInf(point.Latitude, 0) {
		return errors.New("latitude must be finite")
	}
	if math.IsNaN(point.Longitude) || math.IsInf(point.Longitude, 0) {
		return errors.New("longitude must be finite")
	}
	if point.Latitude < -90 || point.Latitude > 90 {
		return fmt.Errorf("latitude %.6f is outside [-90, 90]", point.Latitude)
	}
	if point.Longitude < -180 || point.Longitude > 180 {
		return fmt.Errorf("longitude %.6f is outside [-180, 180]", point.Longitude)
	}
	return nil
}

func (b BoundingBox) Validate() error {
	if err := ValidatePoint(model.GeoPoint{Latitude: b.MinLatitude, Longitude: b.MinLongitude}); err != nil {
		return fmt.Errorf("invalid minimum corner: %w", err)
	}
	if err := ValidatePoint(model.GeoPoint{Latitude: b.MaxLatitude, Longitude: b.MaxLongitude}); err != nil {
		return fmt.Errorf("invalid maximum corner: %w", err)
	}
	if b.MinLatitude >= b.MaxLatitude {
		return errors.New("minimum latitude must be less than maximum latitude")
	}
	if b.MinLongitude >= b.MaxLongitude {
		return errors.New("minimum longitude must be less than maximum longitude")
	}
	return nil
}

func (b BoundingBox) Center() model.GeoPoint {
	return model.GeoPoint{
		Latitude:  (b.MinLatitude + b.MaxLatitude) / 2,
		Longitude: (b.MinLongitude + b.MaxLongitude) / 2,
	}
}

func (b BoundingBox) Contains(point model.GeoPoint) bool {
	return point.Latitude >= b.MinLatitude && point.Latitude <= b.MaxLatitude &&
		point.Longitude >= b.MinLongitude && point.Longitude <= b.MaxLongitude
}

// Projector uses an equirectangular projection around one fixed origin. It is
// suitable for the city-sized area used by this exercise.
type Projector struct {
	origin          model.GeoPoint
	originLatitude  float64
	originLongitude float64
	longitudeScale  float64
}

func NewProjector(origin model.GeoPoint) (Projector, error) {
	if err := ValidatePoint(origin); err != nil {
		return Projector{}, fmt.Errorf("invalid projection origin: %w", err)
	}

	latitudeRadians := degreesToRadians(origin.Latitude)
	return Projector{
		origin:          origin,
		originLatitude:  latitudeRadians,
		originLongitude: degreesToRadians(origin.Longitude),
		longitudeScale:  math.Cos(latitudeRadians),
	}, nil
}

func (p Projector) Origin() model.GeoPoint {
	return p.origin
}

func (p Projector) Project(point model.GeoPoint) (model.Point2D, error) {
	if err := ValidatePoint(point); err != nil {
		return model.Point2D{}, err
	}

	return model.Point2D{
		X: EarthRadiusMeters * (degreesToRadians(point.Longitude) - p.originLongitude) * p.longitudeScale,
		Y: EarthRadiusMeters * (degreesToRadians(point.Latitude) - p.originLatitude),
	}, nil
}

func degreesToRadians(degrees float64) float64 {
	return degrees * math.Pi / 180
}
