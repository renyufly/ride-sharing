// Package matcher contains the exact distance and tie-breaking rules shared by
// all strategy A matcher implementations.
package matcher

import (
	"errors"
	"fmt"
	"math"

	"ride-sharing/internal/model"
)

type IndexStats struct {
	Kind           string `json:"kind"`
	EntryCount     int    `json:"entryCount"`
	EntrySizeBytes uint64 `json:"entrySizeBytes"`
	EstimatedBytes uint64 `json:"estimatedBytes"`
}

func CopyAndValidateRiders(riders []model.Rider) ([]model.Rider, error) {
	if len(riders) == 0 {
		return nil, errors.New("at least one rider is required")
	}

	seenUIDs := make(map[uint64]struct{}, len(riders))
	ownedRiders := make([]model.Rider, len(riders))
	for index, rider := range riders {
		if !IsFinitePoint(rider.Point) {
			return nil, fmt.Errorf("rider %d has a non-finite projected point", rider.UID)
		}
		if _, exists := seenUIDs[rider.UID]; exists {
			return nil, fmt.Errorf("duplicate rider UID %d", rider.UID)
		}
		seenUIDs[rider.UID] = struct{}{}
		ownedRiders[index] = rider
	}
	return ownedRiders, nil
}

func DistanceSquared(first, second model.Point2D) float64 {
	deltaX := first.X - second.X
	deltaY := first.Y - second.Y
	return deltaX*deltaX + deltaY*deltaY
}

func IsBetter(candidateDistance float64, candidateUID uint64, bestDistance float64, bestUID uint64) bool {
	return candidateDistance < bestDistance ||
		(candidateDistance == bestDistance && candidateUID < bestUID)
}

func IsFinitePoint(point model.Point2D) bool {
	return !math.IsNaN(point.X) && !math.IsInf(point.X, 0) &&
		!math.IsNaN(point.Y) && !math.IsInf(point.Y, 0)
}
