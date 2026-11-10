package matching

import (
	"fmt"
	"math"

	domainmatching "ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
)

// pendingOffer 一个尚未结束的订单邀请流程 (记录所有分配尝试)
type pendingOffer struct {
	ready     *candidateResult
	selection balancedSelection
	tried     map[uint64]struct{}
}

type offerScanInput struct {
	candidates         []domainmatching.Candidate
	maxDistanceSquared float64
	loads              []uint64
	reserved           []uint64
	uidIndex           map[uint64]int
	tried              map[uint64]struct{}
	maxOrdersPerRider  int
}

type offerScanResult struct {
	candidate            domainmatching.Candidate
	riderIndex           int
	capacityFiltered     uint64
	blockedByReservation bool
}

// scanOfferCandidates 
func scanOfferCandidates(
	input offerScanInput,
	outsideOnly bool,
) (offerScanResult, error) {
	result := offerScanResult{riderIndex: -1}

	for _, candidate := range input.candidates {
		withinDistance :=
			candidate.DistanceSquaredMeters <= input.maxDistanceSquared

		if outsideOnly {
			if withinDistance {
				continue
			}
		} else if !withinDistance {
			// 候选已按距离排序。
			break
		}

		if _, tried := input.tried[candidate.RiderUID]; tried {
			continue
		}

		index, exists := input.uidIndex[candidate.RiderUID]
		if !exists {
			return offerScanResult{}, fmt.Errorf(
				"candidate rider UID %d is unknown",
				candidate.RiderUID,
			)
		}

		load := input.loads[index] + input.reserved[index]
		if input.maxOrdersPerRider > 0 &&
			load >= uint64(input.maxOrdersPerRider) {
			result.capacityFiltered++
			if input.loads[index] < uint64(input.maxOrdersPerRider) &&
				input.reserved[index] > 0 {
				result.blockedByReservation = true
			}
			continue
		}

		if result.riderIndex == -1 ||
			load < input.loads[result.riderIndex]+input.reserved[result.riderIndex] ||
			(load == input.loads[result.riderIndex]+input.reserved[result.riderIndex] &&
				domainmatching.CandidateLess(candidate, result.candidate)) {
			result.candidate = candidate
			result.riderIndex = index
		}
	}
	return result, nil
}

// chooseOfferCandidate 只负责选择骑手
func chooseOfferCandidate(
	currentOrder order.Order,
	candidates []domainmatching.Candidate,
	loads []uint64,
	reserved []uint64,
	uidIndex map[uint64]int,
	tried map[uint64]struct{},
	maxExtraDistanceMeters float64,
	maxOrdersPerRider int,
) (balancedSelection, error) {
	if len(candidates) == 0 {
		return balancedSelection{}, nil
	}

	nearestSquared := candidates[0].DistanceSquaredMeters
	maxDistanceSquared := nearestSquared
	if maxExtraDistanceMeters > 0 {
		extra := maxExtraDistanceMeters
		maxDistanceSquared += 2*math.Sqrt(nearestSquared)*extra + extra*extra
	}

	input := offerScanInput{
		candidates:         candidates,
		maxDistanceSquared: maxDistanceSquared,
		loads:              loads,
		reserved:           reserved,
		uidIndex:           uidIndex,
		tried:              tried,
		maxOrdersPerRider:  maxOrdersPerRider,
	}

	inside, err := scanOfferCandidates(input, false)
	if err != nil {
		return balancedSelection{}, err
	}

	selected := inside
	capacityFiltered := inside.capacityFiltered
	blockedByReservation := inside.blockedByReservation

	if selected.riderIndex == -1 && len(tried) > 0 {
		outside, err := scanOfferCandidates(input, true)
		if err != nil {
			return balancedSelection{}, err
		}

		capacityFiltered += outside.capacityFiltered
		blockedByReservation =
			blockedByReservation || outside.blockedByReservation

		if outside.riderIndex != -1 {
			selected = outside
		}
	}

	if selected.riderIndex == -1 {
		return balancedSelection{
			capacityFiltered:     capacityFiltered,
			blockedByReservation: blockedByReservation,
		}, nil
	}

	return balancedSelection{
		assignment: domainmatching.Assignment{
			OrderID:               currentOrder.ID,
			Sequence:              currentOrder.Sequence,
			RiderUID:              selected.candidate.RiderUID,
			DistanceSquaredMeters: selected.candidate.DistanceSquaredMeters,
		},
		riderIndex:       selected.riderIndex,
		assigned:         true,
		capacityFiltered: capacityFiltered,
	}, nil

}


func (p *pendingOffer) makeOfferJob() offerJob {
	selection := p.selection
	p.tried[selection.assignment.RiderUID] = struct{}{}

	return offerJob{
		order: p.ready.order,
		candidate: domainmatching.Candidate{
			RiderUID:              selection.assignment.RiderUID,
			DistanceSquaredMeters: selection.assignment.DistanceSquaredMeters,
		},
	}
}


func (p *pendingOffer) validateOfferResult(result offerResult) error {
	if result.job.order.ID != p.ready.order.ID ||
		result.job.order.Sequence != p.ready.order.Sequence ||
		result.job.candidate.RiderUID != p.selection.assignment.RiderUID {
		return fmt.Errorf(
			"offer result does not match order %d rider %d",
			p.ready.order.ID,
			p.selection.assignment.RiderUID,
		)
	}

	return nil
}