// Package bruteforce implements the serial, exact nearest-rider baseline.
package bruteforce

import (
	"errors"
	"fmt"

	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/model"
)

type Matcher struct {
	riders []model.Rider
}

// New validates and copies the static rider set. Copying prevents a caller
// from changing coordinates while a run is in progress.
func New(riders []model.Rider) (Matcher, error) {
	ownedRiders, err := matchrule.CopyAndValidateRiders(riders)
	if err != nil {
		return Matcher{}, err
	}
	return Matcher{riders: ownedRiders}, nil
}

// Match scans every rider and returns the exact nearest rider in the projected
// coordinate system. Exact distance ties are resolved by the smaller UID.
func (m Matcher) Match(order model.Order) (model.Assignment, error) {
	if len(m.riders) == 0 {
		return model.Assignment{}, errors.New("matcher has no riders")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		return model.Assignment{}, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}

	bestRider := m.riders[0]
	bestDistance := matchrule.DistanceSquared(order.Point, bestRider.Point)
	for index := 1; index < len(m.riders); index++ {
		candidate := m.riders[index]
		candidateDistance := matchrule.DistanceSquared(order.Point, candidate.Point)
		if matchrule.IsBetter(candidateDistance, candidate.UID, bestDistance, bestRider.UID) {
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
