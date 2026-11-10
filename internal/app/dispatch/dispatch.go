package dispatch

import (
	"context"
	"errors"
	"math"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
	"time"

	"github.com/google/uuid"
)

var ErrNoRiderAccepted = errors.New("no candidate rider accepted the order")


// OfferOne 邀请一位骑手，返回这次邀请的最终状态 (拒单、超时和发送失败)。
func (s *Service) OfferOne(
	ctx context.Context,
	currentOrder order.Order,
	riderUID uint64,
	sender Sender,
	responseTimeout time.Duration,
) (domaindispatch.Attempt, error) {
	if responseTimeout <= 0 {
		return domaindispatch.Attempt{}, errors.New("response timeout must be positive")
	}
	if err := ctx.Err(); err != nil {
		return domaindispatch.Attempt{}, err
	}

	attempt := domaindispatch.Attempt{
		OrderID:   currentOrder.ID,
		RiderUID:  riderUID,
		RequestID: uuid.NewString(),
		Status:    domaindispatch.StatusPending,
	}

	reply := s.register(currentOrder.ID, riderUID, attempt.RequestID)

	err := sender.SendOffer(ctx, riderUID, Offer{
		Order:     currentOrder,
		RequestID: attempt.RequestID,
	})
	if err != nil {
		if s.expire(attempt.RequestID) {
			attempt.Status = domaindispatch.StatusDeliveryFailed
			return attempt, nil
		}

		if <-reply {
			attempt.Status = domaindispatch.StatusAccepted
		} else {
			attempt.Status = domaindispatch.StatusDeclined
		}
		return attempt, nil
	}

	timer := time.NewTimer(responseTimeout)
	defer timer.Stop()

	var accepted bool
	select {
	case accepted = <-reply:
	case <-timer.C:
		if s.expire(attempt.RequestID) {
			attempt.Status = domaindispatch.StatusTimedOut
			return attempt, nil
		}
		accepted = <-reply
	case <-ctx.Done():
		if s.expire(attempt.RequestID) {
			return attempt, ctx.Err()
		}
		accepted = <-reply
	}

	if accepted {
		attempt.Status = domaindispatch.StatusAccepted
	} else {
		attempt.Status = domaindispatch.StatusDeclined
	}
	return attempt, nil
}


// Dispatch 向候选骑手发起接单邀请。
//
// 该方法首先查找距离订单最近的 topK 名骑手，然后依次发送邀请。
// 每位骑手最多有 responseTimeout 的时间回复。
// 如果骑手拒单、超时或邀请发送失败，则继续尝试下一位骑手。
//
// 参数：
//   - order：需要派单的订单。
//   - finder：用于查找候选骑手的接口。
//   - sender：用于发送接单邀请的接口。
//   - topK：最多尝试的候选骑手数量。
//   - responseTimeout：每位骑手的回复等待时间。
//   - loads: 骑手当前负载  (riderUID -> orderCount)
//   - maxExtraDistanceMeters: 相对最近骑手允许增加的距离 (topK)
//   - maxOrdersPerRider: 单名骑手最多分配多少订单
//
// 返回值：
//   - matching.Assignment：接单成功时的订单分配结果，失败时为{}。
//   - []domaindispatch.Attempt：已记录的派单尝试。
func (s *Service) Dispatch(ctx context.Context, order order.Order, 
	finder CandidateFinder, sender Sender, 
	topK int, responseTimeOut time.Duration,
	maxExtraDistanceMeters float64,
	maxOrdersPerRider int,
	) (matching.Assignment, []domaindispatch.Attempt, error) {
		if topK <= 0 || responseTimeOut <= 0 {
			return matching.Assignment{}, nil, errors.New("topK and responseTimeout must be positive")
		}

		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()

		// 检查orderID是否在之前已被分配过 
		if previous, ok := s.assigned[order.ID]; ok {
			return previous, nil, nil
		}

		// 找出距离订单最近的 Top-K 个骑手
		candidates, err := finder.TopKInto(order, topK, nil)
		if err != nil {
			return matching.Assignment{}, nil, err
		}

		candidates = append([]matching.Candidate(nil), candidates...)
		matching.SortCandidates(candidates) // 按距离从近到远排列

		if len(candidates) == 0 {
			return matching.Assignment{}, nil, ErrNoRiderAccepted
		}

		// 计算允许派单的最大距离
		nearestSquared := candidates[0].DistanceSquaredMeters
		maxDistanceSquared := nearestSquared
		if maxExtraDistanceMeters > 0 {
			extra := maxExtraDistanceMeters
			maxDistanceSquared += 2*math.Sqrt(nearestSquared)*extra + extra*extra
		}

		// 
		tried := make(map[uint64]struct{}, len(candidates))

		attempts := make([]domaindispatch.Attempt, 0, len(candidates))
		// 依次发起派单attempt
		for {
			candidate, ok := nextBalancedCandidate(
				candidates, s.loads, tried, maxDistanceSquared, maxOrdersPerRider,
			)
			if !ok {
				break
			}
			tried[candidate.RiderUID] = struct{}{}
			if err := ctx.Err(); err != nil {
				return matching.Assignment{}, attempts, err
			}

			attempt, err := s.OfferOne(
				ctx,
				order,
				candidate.RiderUID,
				sender,
				responseTimeOut,
			)
			if attempt.RequestID != "" {
				attempts = append(attempts, attempt)
			}
			if err != nil {
				return matching.Assignment{}, attempts, err
			}
			if attempt.Status != domaindispatch.StatusAccepted {
				continue
			}
			
			
			assignment := matching.Assignment{
				OrderID:               order.ID,
				Sequence:              order.Sequence,
				RiderUID:              candidate.RiderUID,
				DistanceSquaredMeters: candidate.DistanceSquaredMeters,
			}
			s.loads[assignment.RiderUID]++
			s.assigned[order.ID] = assignment

			return assignment, attempts, nil
		}

		if err := ctx.Err(); err != nil {
			return matching.Assignment{}, attempts, err
		}
		
		return matching.Assignment{}, attempts, ErrNoRiderAccepted
}

// 在符合距离约束、尚未尝试、未达到订单上限的骑手中，选出负载loads最低的一位
// 未尝试且未满载 → 负载最小 → 距离最近
func nextBalancedCandidate(
	candidates []matching.Candidate,
	loads map[uint64]uint64,
	tried map[uint64]struct{},
	maxDistanceSquared float64,
	maxOrdersPerRider int,
) (matching.Candidate, bool) {
	var selected matching.Candidate
	found := false

	for _, candidate := range candidates {
		if candidate.DistanceSquaredMeters > maxDistanceSquared {
			break 
		}
		if _, attempted := tried[candidate.RiderUID]; attempted {
			continue
		}

		load := loads[candidate.RiderUID]
		if maxOrdersPerRider > 0 && load >= uint64(maxOrdersPerRider) {
			continue
		}

		if !found ||
			load < loads[selected.RiderUID] ||
			(load == loads[selected.RiderUID] &&
				matching.CandidateLess(candidate, selected)) {
			selected = candidate
			found = true
		}
	}
	return selected, found
}