// Package bruteforce implements the serial, exact nearest-rider baseline.
package bruteforce

import (
	"errors"
	"fmt"
	"math"

	"ride-sharing/internal/model"
)

type Matcher struct {
	riders []model.Rider
}

// New validates and copies the static rider set. Copying prevents a caller
// from changing coordinates while a run is in progress.
func New(riders []model.Rider) (Matcher, error) {
	if len(riders) == 0 {
		return Matcher{}, errors.New("at least one rider is required")
	}

	seenUIDs := make(map[uint64]struct{}, len(riders))
	ownedRiders := make([]model.Rider, len(riders))
	for index, rider := range riders {
		if !isFinitePoint(rider.Point) {
			return Matcher{}, fmt.Errorf("rider %d has a non-finite projected point", rider.UID)
		}
		if _, exists := seenUIDs[rider.UID]; exists {
			return Matcher{}, fmt.Errorf("duplicate rider UID %d", rider.UID)
		}
		seenUIDs[rider.UID] = struct{}{}
		ownedRiders[index] = rider
	}

	return Matcher{riders: ownedRiders}, nil
}

// Match scans every rider and returns the exact nearest rider in the projected
// coordinate system. Exact distance ties are resolved by the smaller UID.
func (m Matcher) Match(order model.Order) (model.Assignment, error) {
	if len(m.riders) == 0 {
		return model.Assignment{}, errors.New("matcher has no riders")
	}
	if !isFinitePoint(order.Point) {
		return model.Assignment{}, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}

	bestRider := m.riders[0]
	bestDistance := distanceSquared(order.Point, bestRider.Point)
	for index := 1; index < len(m.riders); index++ {
		candidate := m.riders[index]
		candidateDistance := distanceSquared(order.Point, candidate.Point)
		if candidateDistance < bestDistance ||
			(candidateDistance == bestDistance && candidate.UID < bestRider.UID) {
			bestRider = candidate
			bestDistance = candidateDistance
		}
	}

	return model.Assignment{
		OrderID:               order.ID,
		Sequence:              order.Sequence,
		RiderUID:              bestRider.UID,
		DistanceSquaredMeters: bestDistance,
	}, nil
}

func distanceSquared(first, second model.Point2D) float64 {
	deltaX := first.X - second.X
	deltaY := first.Y - second.Y
	return deltaX*deltaX + deltaY*deltaY
}

func isFinitePoint(point model.Point2D) bool {
	return !math.IsNaN(point.X) && !math.IsInf(point.X, 0) &&
		!math.IsNaN(point.Y) && !math.IsInf(point.Y, 0)
}
