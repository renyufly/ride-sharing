package matching

import (
	"fmt"

	domainmatching "ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
)

type RiderLoad struct {
	Active   uint64
	Reserved uint64
}

// SelectLiveCandidate requires the matcher and riderLoads to come from the same snapshot.
func SelectLiveCandidate(
	currentOrder order.Order,
	matcher CandidateMatcher,
	riderLoads map[uint64]RiderLoad,
	tried map[uint64]struct{},
	topK int,
	maxExtraDistanceMeters float64,
	maxOrdersPerRider int,
) (domainmatching.Assignment, bool, error) {
	if len(riderLoads) == 0 {
		return domainmatching.Assignment{}, false, nil
	}

	candidates, err := matcher.TopKInto(currentOrder, min(topK, len(riderLoads)), nil)
	if err != nil {
		return domainmatching.Assignment{}, false, fmt.Errorf("find order candidates: %w", err)
	}

	loads := make([]uint64, len(candidates))
	reserved := make([]uint64, len(candidates))
	uidIndex := make(map[uint64]int, len(candidates))
	for i, candidate := range candidates {
		load, ok := riderLoads[candidate.RiderUID]
		if !ok {
			return domainmatching.Assignment{}, false,
				fmt.Errorf("candidate rider %d missing from load snapshot", candidate.RiderUID)
		}

		loads[i] = load.Active
		reserved[i] = load.Reserved
		uidIndex[candidate.RiderUID] = i
	}

	selected, err := chooseOfferCandidate(
		currentOrder,
		candidates,
		loads,
		reserved,
		uidIndex,
		tried,
		maxExtraDistanceMeters,
		maxOrdersPerRider,
	)
	return selected.assignment, selected.assigned, err
}