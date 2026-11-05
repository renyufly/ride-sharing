package dispatch

import (
	"context"
	"errors"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
	"time"

	"github.com/google/uuid"
)

// Dispatch 向候选骑手发起接单邀请。
//
// 该方法首先查找距离订单最近的 topK 名骑手，然后依次发送邀请。
// 每位骑手最多有 responseTimeout 的时间回复。
// 如果骑手拒单、超时或邀请发送失败，则继续尝试下一位骑手。
//
// 参数：
//   - ctx：控制派单流程的取消及整体生命周期。
//   - order：需要派单的订单。
//   - finder：用于查找候选骑手的接口。
//   - sender：用于发送接单邀请的接口。
//   - topK：最多尝试的候选骑手数量。
//   - responseTimeout：每位骑手的回复等待时间。
//
// 返回值：
//   - matching.Assignment：接单成功时的订单分配结果，失败时为{}。
//   - []domaindispatch.Attempt：已记录的派单尝试。
func (s *Service) Dispatch(ctx context.Context, order order.Order, 
	finder CandidateFinder, sender Sender, 
	topK int, responseTimeOut time.Duration,
	) (matching.Assignment, []domaindispatch.Attempt, error) {

		if topK <= 0 || responseTimeOut <= 0 {
			return matching.Assignment{}, nil, errors.New("topK and responseTimeout must be positive")
		}

		// 找出距离订单最近的 Top-K 个骑手
		candidates, err := finder.TopKInto(order, topK, nil)
		if err != nil {
			return matching.Assignment{}, nil, err
		}

		candidates = append([]matching.Candidate(nil), candidates...)
		matching.SortCandidates(candidates)

		attempts := make([]domaindispatch.Attempt, 0, len(candidates))
		// 依次发起派单attempt
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return matching.Assignment{}, attempts, err
			}

			requestID := uuid.NewString()
			attempt := domaindispatch.Attempt{
				OrderID: order.ID,
				RiderUID: candidate.RiderUID,
				RequestID: requestID,
				Status: domaindispatch.StatusPending,
			}

			// 登记等待中的接单请求
			reply := s.register(attempt.OrderID, attempt.RiderUID, requestID)

			// 向骑手发送接单邀请
			if err := sender.SendOffer(ctx, attempt.RiderUID, Offer{
				Order: order,
				RequestID: requestID,
			}); err != nil {
				s.expire(requestID)
				attempt.Status = domaindispatch.StatusDeliveryFailed

				attempts = append(attempts, attempt)
				// 继续下一个候选骑手分配
				continue
			}

			// 成功向一名骑手Send Offer, 设置超时响应接单时间
			timer := time.NewTimer(responseTimeOut)
			var accepted bool
			select {
				case accepted = <-reply:
					// 收到骑手回复 (可以是true也可是false)
					timer.Stop()
				case <-timer.C:
					// 等待超时
					if s.expire(requestID) {
						attempt.Status = domaindispatch.StatusTimedOut
						
						attempts = append(attempts, attempt)
						continue
					}
					// 如果超时和骑手回复 几乎同时发生
					accepted = <-reply
				case <-ctx.Done():
					// 请求取消
					timer.Stop()
					s.expire(requestID)
					return matching.Assignment{}, attempts, ctx.Err()
			}

			if (!accepted) {
				attempt.Status = domaindispatch.StatusDeclined
				
				attempts = append(attempts, attempt)
				continue
			}

			// accepted == true
			attempt.Status = domaindispatch.StatusAccepted
			attempts = append(attempts, attempt)

			return matching.Assignment{
				OrderID: order.ID,
				Sequence: order.Sequence,
				RiderUID: candidate.RiderUID,
				DistanceSquaredMeters: candidate.DistanceSquaredMeters,
			}, attempts, nil
		}

		return matching.Assignment{}, attempts, 
		errors.New("no candidate rider accepted the order")
}