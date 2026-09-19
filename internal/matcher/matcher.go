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

type Candidate struct {
	RiderUID              uint64  `json:"riderUid"`
	DistanceSquaredMeters float64 `json:"distanceSquaredMeters"`
}

func CandidateLess(first, second Candidate) bool {
	return IsBetter(
		first.DistanceSquaredMeters,
		first.RiderUID,
		second.DistanceSquaredMeters,
		second.RiderUID,
	)
}

// SortCandidates uses allocation-free insertion sort. Top-K is deliberately
// small, so O(K²) comparisons are cheaper than reflection-backed sort.Slice
// and avoid per-order heap allocations on the hot path.
func SortCandidates(candidates []Candidate) {
	for index := 1; index < len(candidates); index++ {
		value := candidates[index]
		position := index
		for position > 0 && CandidateLess(value, candidates[position-1]) {
			candidates[position] = candidates[position-1]
			position--
		}
		candidates[position] = value
	}
}

// RetainCandidate maintains a max-heap containing the best limit candidates
// without interface conversions or allocations.
func RetainCandidate(candidates []Candidate, limit int, candidate Candidate) []Candidate {
	if len(candidates) < limit {
		candidates = append(candidates, candidate)
		index := len(candidates) - 1
		for index > 0 {
			parent := (index - 1) / 2
			if !CandidateLess(candidates[parent], candidates[index]) {
				break
			}
			candidates[parent], candidates[index] = candidates[index], candidates[parent]
			index = parent
		}
		return candidates
	}
	if !CandidateLess(candidate, candidates[0]) {
		return candidates
	}
	candidates[0] = candidate
	for index := 0; ; {
		left := index*2 + 1
		if left >= len(candidates) {
			break
		}
		worst := left
		right := left + 1
		if right < len(candidates) && CandidateLess(candidates[worst], candidates[right]) {
			worst = right
		}
		if !CandidateLess(candidates[index], candidates[worst]) {
			break
		}
		candidates[index], candidates[worst] = candidates[worst], candidates[index]
		index = worst
	}
	return candidates
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
