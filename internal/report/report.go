// Package report accumulates assignment metrics without retaining individual
// assignments and produces the deterministic Bottom 10 rider report.
package report

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"

	"ride-sharing/internal/model"
)

const (
	bottomLimit                = 10
	distanceHistogramMaxMeters = 50_000
)

type RiderOrderCount struct {
	RiderUID   uint64 `json:"riderUid"`
	OrderCount uint64 `json:"orderCount"`
}

type Summary struct {
	AssignmentCount           uint64            `json:"assignmentCount"`
	RiderOrderCountSum        uint64            `json:"riderOrderCountSum"`
	Bottom10                  []RiderOrderCount `json:"bottom10"`
	ZeroRiderCount            int               `json:"zeroRiderCount"`
	MinOrders                 uint64            `json:"minOrders"`
	MeanOrders                float64           `json:"meanOrders"`
	MaxOrders                 uint64            `json:"maxOrders"`
	VarianceOrders            float64           `json:"varianceOrders"`
	StdDevOrders              float64           `json:"stdDevOrders"`
	CoefficientOfVariation    float64           `json:"coefficientOfVariation"`
	AverageDistanceMeters     float64           `json:"averageDistanceMeters"`
	P95DistanceMeters         float64           `json:"p95DistanceMeters"`
	MaxDistanceMeters         float64           `json:"maxDistanceMeters"`
	DistanceHistogramOverflow uint64            `json:"distanceHistogramOverflow"`
}

type Accumulator struct {
	riderIndex                map[uint64]int
	riderCounts               []RiderOrderCount
	assignmentCount           uint64
	distanceSumMicrometers    uint64
	maxDistanceMeters         float64
	distanceHistogram         []uint64
	distanceHistogramOverflow uint64
}

func New(riders []model.Rider) (*Accumulator, error) {
	if len(riders) == 0 {
		return nil, errors.New("at least one rider is required for reporting")
	}

	accumulator := &Accumulator{
		riderIndex:        make(map[uint64]int, len(riders)),
		riderCounts:       make([]RiderOrderCount, len(riders)),
		distanceHistogram: make([]uint64, distanceHistogramMaxMeters+1),
	}
	for index, rider := range riders {
		if _, exists := accumulator.riderIndex[rider.UID]; exists {
			return nil, fmt.Errorf("duplicate rider UID %d", rider.UID)
		}
		accumulator.riderIndex[rider.UID] = index
		accumulator.riderCounts[index].RiderUID = rider.UID
	}
	return accumulator, nil
}

// Fork creates an empty worker-owned accumulator that shares only the
// immutable UID-to-index map. Counts and distance metrics remain private.
func (a *Accumulator) Fork() *Accumulator {
	riderCounts := make([]RiderOrderCount, len(a.riderCounts))
	for index, rider := range a.riderCounts {
		riderCounts[index].RiderUID = rider.RiderUID
	}
	return &Accumulator{
		riderIndex:        a.riderIndex,
		riderCounts:       riderCounts,
		distanceHistogram: make([]uint64, len(a.distanceHistogram)),
	}
}

// Observe records one final assignment. It performs the only square root in
// the matching/reporting path, after the winning rider has been selected.
func (a *Accumulator) Observe(assignment model.Assignment) error {
	index, exists := a.riderIndex[assignment.RiderUID]
	if !exists {
		return fmt.Errorf("assignment references unknown rider UID %d", assignment.RiderUID)
	}
	if math.IsNaN(assignment.DistanceSquaredMeters) || math.IsInf(assignment.DistanceSquaredMeters, 0) || assignment.DistanceSquaredMeters < 0 {
		return fmt.Errorf("assignment for order %d has invalid squared distance %v", assignment.OrderID, assignment.DistanceSquaredMeters)
	}

	distanceMeters := math.Sqrt(assignment.DistanceSquaredMeters)
	distanceMicrometers := math.Round(distanceMeters * 1_000_000)
	if distanceMicrometers > float64(^uint64(0)-a.distanceSumMicrometers) {
		return fmt.Errorf("assignment distance sum overflows uint64 micrometers")
	}
	a.riderCounts[index].OrderCount++
	a.assignmentCount++
	a.distanceSumMicrometers += uint64(distanceMicrometers)
	if distanceMeters > a.maxDistanceMeters {
		a.maxDistanceMeters = distanceMeters
	}

	bucket := int(math.Ceil(distanceMeters))
	if bucket > distanceHistogramMaxMeters {
		a.distanceHistogramOverflow++
	} else {
		a.distanceHistogram[bucket]++
	}
	return nil
}

// Merge combines a worker-owned accumulator into the receiver. Both
// accumulators must have been created from the same ordered rider set. The
// caller must not mutate source concurrently and should treat it as handed off
// after this call.
func (a *Accumulator) Merge(source *Accumulator) error {
	if source == nil {
		return errors.New("cannot merge a nil accumulator")
	}
	if len(a.riderCounts) != len(source.riderCounts) || len(a.distanceHistogram) != len(source.distanceHistogram) {
		return errors.New("cannot merge accumulators with different shapes")
	}
	if source.distanceSumMicrometers > ^uint64(0)-a.distanceSumMicrometers {
		return errors.New("cannot merge distance sums: uint64 micrometers overflow")
	}
	for index := range a.riderCounts {
		if a.riderCounts[index].RiderUID != source.riderCounts[index].RiderUID {
			return fmt.Errorf("cannot merge rider index %d: UID %d != %d", index, a.riderCounts[index].RiderUID, source.riderCounts[index].RiderUID)
		}
	}
	for index := range a.riderCounts {
		a.riderCounts[index].OrderCount += source.riderCounts[index].OrderCount
	}
	for index, count := range source.distanceHistogram {
		a.distanceHistogram[index] += count
	}
	a.assignmentCount += source.assignmentCount
	a.distanceSumMicrometers += source.distanceSumMicrometers
	a.distanceHistogramOverflow += source.distanceHistogramOverflow
	if source.maxDistanceMeters > a.maxDistanceMeters {
		a.maxDistanceMeters = source.maxDistanceMeters
	}
	return nil
}

func (a *Accumulator) Summary() Summary {
	mean := float64(a.assignmentCount) / float64(len(a.riderCounts))
	minOrders := a.riderCounts[0].OrderCount
	var maxOrders uint64
	var countSum uint64
	var squaredDeviationSum float64
	zeroRiders := 0

	for _, rider := range a.riderCounts {
		countSum += rider.OrderCount
		if rider.OrderCount == 0 {
			zeroRiders++
		}
		if rider.OrderCount < minOrders {
			minOrders = rider.OrderCount
		}
		if rider.OrderCount > maxOrders {
			maxOrders = rider.OrderCount
		}
		difference := float64(rider.OrderCount) - mean
		squaredDeviationSum += difference * difference
	}

	variance := squaredDeviationSum / float64(len(a.riderCounts))
	stdDev := math.Sqrt(variance)
	coefficientOfVariation := 0.0
	if mean != 0 {
		coefficientOfVariation = stdDev / mean
	}
	averageDistance := 0.0
	if a.assignmentCount != 0 {
		averageDistance = float64(a.distanceSumMicrometers) / 1_000_000 / float64(a.assignmentCount)
	}

	return Summary{
		AssignmentCount:           a.assignmentCount,
		RiderOrderCountSum:        countSum,
		Bottom10:                  bottomRiders(a.riderCounts, bottomLimit),
		ZeroRiderCount:            zeroRiders,
		MinOrders:                 minOrders,
		MeanOrders:                mean,
		MaxOrders:                 maxOrders,
		VarianceOrders:            variance,
		StdDevOrders:              stdDev,
		CoefficientOfVariation:    coefficientOfVariation,
		AverageDistanceMeters:     averageDistance,
		P95DistanceMeters:         a.percentile95(),
		MaxDistanceMeters:         a.maxDistanceMeters,
		DistanceHistogramOverflow: a.distanceHistogramOverflow,
	}
}

func (a *Accumulator) percentile95() float64 {
	if a.assignmentCount == 0 {
		return 0
	}
	targetRank := uint64(math.Ceil(float64(a.assignmentCount) * 0.95))
	var cumulative uint64
	for bucket, count := range a.distanceHistogram {
		cumulative += count
		if cumulative >= targetRank {
			return float64(bucket)
		}
	}
	// If P95 falls into the overflow bucket, return the exact observed maximum
	// as a conservative bound and expose the overflow count in the summary.
	return a.maxDistanceMeters
}

func bottomRiders(riders []RiderOrderCount, limit int) []RiderOrderCount {
	if limit > len(riders) {
		limit = len(riders)
	}
	candidates := make(maxRiderHeap, 0, limit)
	for _, rider := range riders {
		if len(candidates) < limit {
			heap.Push(&candidates, rider)
			continue
		}
		if riderLess(rider, candidates[0]) {
			candidates[0] = rider
			heap.Fix(&candidates, 0)
		}
	}

	result := append([]RiderOrderCount(nil), candidates...)
	sort.Slice(result, func(first, second int) bool {
		return riderLess(result[first], result[second])
	})
	return result
}

func riderLess(first, second RiderOrderCount) bool {
	if first.OrderCount != second.OrderCount {
		return first.OrderCount < second.OrderCount
	}
	return first.RiderUID < second.RiderUID
}

// maxRiderHeap keeps the worst currently retained Bottom 10 candidate at the
// root: higher order count first, then higher UID.
type maxRiderHeap []RiderOrderCount

func (h maxRiderHeap) Len() int { return len(h) }

func (h maxRiderHeap) Less(first, second int) bool {
	return riderLess(h[second], h[first])
}

func (h maxRiderHeap) Swap(first, second int) {
	h[first], h[second] = h[second], h[first]
}

func (h *maxRiderHeap) Push(value any) {
	*h = append(*h, value.(RiderOrderCount))
}

func (h *maxRiderHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
