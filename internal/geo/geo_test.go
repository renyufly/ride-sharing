package geo

import (
	"math"
	"testing"

	"ride-sharing/internal/model"
)

func TestValidatePointRejectsInvalidCoordinates(t *testing.T) {
	tests := []model.GeoPoint{
		{Latitude: -90.01},
		{Latitude: 90.01},
		{Longitude: -180.01},
		{Longitude: 180.01},
		{Latitude: math.NaN()},
		{Longitude: math.Inf(1)},
	}

	for _, point := range tests {
		if err := ValidatePoint(point); err == nil {
			t.Errorf("ValidatePoint(%+v) error = nil", point)
		}
	}
}

func TestBoundingBoxIncludesBoundary(t *testing.T) {
	for _, point := range []model.GeoPoint{
		{Latitude: SanFranciscoBounds.MinLatitude, Longitude: SanFranciscoBounds.MinLongitude},
		{Latitude: SanFranciscoBounds.MaxLatitude, Longitude: SanFranciscoBounds.MaxLongitude},
	} {
		if !SanFranciscoBounds.Contains(point) {
			t.Errorf("SanFranciscoBounds.Contains(%+v) = false", point)
		}
	}
}

func TestProjectorIsDeterministicAndUsesMeters(t *testing.T) {
	projector, err := NewProjector(SanFranciscoBounds.Center())
	if err != nil {
		t.Fatalf("NewProjector() error = %v", err)
	}
	point := model.GeoPoint{Latitude: 37.78, Longitude: -122.42}

	first, err := projector.Project(point)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	second, err := projector.Project(point)
	if err != nil {
		t.Fatalf("Project() second error = %v", err)
	}
	if first != second {
		t.Fatalf("Project() is not deterministic: %+v != %+v", first, second)
	}
	if first.X == 0 || first.Y == 0 {
		t.Fatalf("Project() = %+v, want non-origin meter coordinates", first)
	}
}

func TestProjectionOriginMapsToZero(t *testing.T) {
	origin := SanFranciscoBounds.Center()
	projector, err := NewProjector(origin)
	if err != nil {
		t.Fatalf("NewProjector() error = %v", err)
	}

	point, err := projector.Project(origin)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if point != (model.Point2D{}) {
		t.Fatalf("Project(origin) = %+v, want zero", point)
	}
}

func TestRepeatedCoordinatesProduceIdenticalPoints(t *testing.T) {
	projector, err := NewProjector(SanFranciscoBounds.Center())
	if err != nil {
		t.Fatalf("NewProjector() error = %v", err)
	}
	coordinate := model.GeoPoint{Latitude: 37.768727753110106, Longitude: -122.41345597077878}

	first, err := projector.Project(coordinate)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	second, err := projector.Project(coordinate)
	if err != nil {
		t.Fatalf("Project() second error = %v", err)
	}
	if first != second {
		t.Fatalf("duplicate coordinates projected differently: %+v != %+v", first, second)
	}
}
