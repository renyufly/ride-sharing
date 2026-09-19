package bruteforce

import (
	"math"
	"testing"
	"time"

	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/model"
)

func TestMatchReturnsGlobalNearestRider(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{
		{UID: 20, Point: model.Point2D{X: 10, Y: 10}},
		{UID: 30, Point: model.Point2D{X: 2, Y: 3}},
		{UID: 10, Point: model.Point2D{X: -5, Y: -5}},
	})
	order := model.Order{ID: 1001, Sequence: 7, Point: model.Point2D{X: 1, Y: 1}}

	assignment, err := matcher.Match(order)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.OrderID != order.ID || assignment.Sequence != order.Sequence {
		t.Fatalf("Match() identity = %+v, want order identity %+v", assignment, order)
	}
	if assignment.RiderUID != 30 {
		t.Fatalf("Match() RiderUID = %d, want 30", assignment.RiderUID)
	}
	if assignment.DistanceSquaredMeters != 5 {
		t.Fatalf("Match() distance squared = %f, want 5", assignment.DistanceSquaredMeters)
	}
}

func TestMatchBreaksDistanceTieBySmallerUID(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{
		{UID: 99, Point: model.Point2D{X: 1, Y: 0}},
		{UID: 7, Point: model.Point2D{X: -1, Y: 0}},
		{UID: 3, Point: model.Point2D{X: 0, Y: 1}},
	})

	assignment, err := matcher.Match(model.Order{ID: 1, Point: model.Point2D{}})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 3 {
		t.Fatalf("Match() RiderUID = %d, want smallest tied UID 3", assignment.RiderUID)
	}
}

func TestMatchUsesProjectedPointNotRawLatitudeLongitude(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{
		{
			UID:      1,
			Location: model.GeoPoint{Latitude: 0, Longitude: 0},
			Point:    model.Point2D{X: 100, Y: 100},
		},
		{
			UID:      2,
			Location: model.GeoPoint{Latitude: 80, Longitude: 170},
			Point:    model.Point2D{X: 1, Y: 1},
		},
	})
	order := model.Order{
		Pickup: model.GeoPoint{Latitude: 0, Longitude: 0},
		Point:  model.Point2D{},
	}

	assignment, err := matcher.Match(order)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 2 {
		t.Fatalf("Match() RiderUID = %d, want projected nearest rider 2", assignment.RiderUID)
	}
}

func TestNewCopiesRiderSet(t *testing.T) {
	riders := []model.Rider{
		{UID: 1, Point: model.Point2D{X: 1}},
		{UID: 2, Point: model.Point2D{X: 10}},
	}
	matcher := mustMatcher(t, riders)
	riders[0].Point.X = 1_000

	assignment, err := matcher.Match(model.Order{Point: model.Point2D{}})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 1 {
		t.Fatalf("Match() RiderUID = %d, want copied rider 1", assignment.RiderUID)
	}
}

func TestNewRejectsInvalidRiders(t *testing.T) {
	tests := []struct {
		name   string
		riders []model.Rider
	}{
		{name: "empty"},
		{name: "duplicate UID", riders: []model.Rider{{UID: 1}, {UID: 1}}},
		{name: "NaN point", riders: []model.Rider{{UID: 1, Point: model.Point2D{X: math.NaN()}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.riders); err == nil {
				t.Fatal("New() error = nil, want validation error")
			}
		})
	}
}

func TestMatchRejectsNonFiniteOrderPoint(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{{UID: 1}})
	_, err := matcher.Match(model.Order{ID: 9, Point: model.Point2D{Y: math.Inf(1)}})
	if err == nil {
		t.Fatal("Match() error = nil, want non-finite point error")
	}
}

func TestRequiredCorrectnessScales(t *testing.T) {
	tests := []struct {
		name       string
		riderCount int
		orderCount int
	}{
		{name: "10 riders x 100 orders", riderCount: 10, orderCount: 100},
		{name: "100 riders x 10000 orders", riderCount: 100, orderCount: 10_000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataGenerator, err := generator.New(42, geo.SanFranciscoBounds)
			if err != nil {
				t.Fatalf("generator.New() error = %v", err)
			}
			riders, err := dataGenerator.GenerateRiders(test.riderCount, generator.DistributionUniform)
			if err != nil {
				t.Fatalf("GenerateRiders() error = %v", err)
			}
			matcher := mustMatcher(t, riders)
			stream, err := dataGenerator.NewOrderStream(test.orderCount, 30*time.Second, generator.ArrivalUniformWindow, generator.DistributionUniform)
			if err != nil {
				t.Fatalf("NewOrderStream() error = %v", err)
			}

			matched := 0
			for {
				order, ok, err := stream.Next()
				if err != nil {
					t.Fatalf("Next() error = %v", err)
				}
				if !ok {
					break
				}
				assignment, err := matcher.Match(order)
				if err != nil {
					t.Fatalf("Match(sequence=%d) error = %v", order.Sequence, err)
				}
				if assignment.Sequence != order.Sequence || assignment.OrderID != order.ID {
					t.Fatalf("assignment identity = %+v, order = %+v", assignment, order)
				}
				matched++
			}
			if matched != test.orderCount {
				t.Fatalf("matched = %d, want %d", matched, test.orderCount)
			}
		})
	}
}

func mustMatcher(t *testing.T, riders []model.Rider) Matcher {
	t.Helper()
	matcher, err := New(riders)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return matcher
}
