package run

import (
	"context"
	"errors"
	"fmt"

	appdispatch "ride-sharing/internal/app/dispatch"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/platform/config"
	"time"
)

type LiveResult struct {
	InputCount uint64		// 成功读取的订单数
	AcceptedCount uint64    // 成功被骑手接受的订单数
	UnassignedCount uint64
	Assignments     []matching.Assignment  // 成功分配的订单记录
	Attempts        []domaindispatch.Attempt  // 骑手接单尝试记录
}

// RunLive 实时派单，逐单派送
// dispathcer 查询候选骑手
// sender	向骑手发送接单请求
func RunLive(
	ctx context.Context,
	cfg config.Config,
	factory InputFactory,
	dispatcher *appdispatch.Service,
	sender appdispatch.Sender,
	responseTimeout time.Duration,
) (result LiveResult, runErr error) {
	if ctx == nil || factory == nil || dispatcher == nil || sender == nil {
		return result, errors.New("live run dependencies cannot be nil")
	}
	if responseTimeout <= 0 {
		return result, errors.New("response timeout must be positive")
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	if cfg.Strategy != config.StrategyBalanced {
		return result, errors.New("live run currently requires balanced strategy and topK")
	}

	if cfg.RunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.RunTimeout)
		defer cancel()
	}

	// 创建订单源和matcher
	prepared, err := factory.Prepare(cfg, nil)
	if err != nil {
		return LiveResult{}, err
	}
	if prepared.SourceCloser != nil {
		defer func() {
			runErr = errors.Join(runErr, prepared.SourceCloser.Close())
		}()
	}

	started := time.Now()
	// 逐笔处理订单
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		currentOrder, ok, err := prepared.OrderSource.Next()
		if err != nil {
			return result, fmt.Errorf("read order: %w", err)
		}
		if !ok {
			break
		}
		result.InputCount++

		// 模拟器生成订单时，每个订单有指定的计划被处理时间，如果未到就等待
		due := started.Add(time.Duration(currentOrder.PlannedArrivalNs))
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return result, ctx.Err()
			}
		}

		// 执行派单
		assignment, attempts, err := dispatcher.Dispatch(
			ctx,
			currentOrder,
			prepared.Matcher,
			sender,
			prepared.Config.TopK,
			responseTimeout,
		)
		result.Attempts = append(result.Attempts, attempts...)

		switch {
		case err == nil:
			result.AcceptedCount++
			result.Assignments = append(result.Assignments, assignment)
		case errors.Is(err, appdispatch.ErrNoRiderAccepted):
			result.UnassignedCount++
		default:
			return result, fmt.Errorf("dispatch order %d: %w", currentOrder.ID, err)
		}
	}

	if result.InputCount != prepared.ExpectedOrders {
		return result, fmt.Errorf(
			"order source ended after %d orders; expected %d",
			result.InputCount, prepared.ExpectedOrders,
		)
	}

	return result, nil
	
	
}