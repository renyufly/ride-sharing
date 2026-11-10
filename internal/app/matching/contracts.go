package matching

import (
	"context"
	"ride-sharing/internal/domain/order"
)

// 未分配订单
type DeferredReason string

const (
	DeferredReasonCapacityExhausted DeferredReason = "capacity_exhausted"
	// 轮到 Coordinator 决策时，已经超过主分配窗口
	DeferredReasonWindowExpired DeferredReason = "window_expired"
	// 没有骑手主动接单
	DeferredReasonNoRiderAccepted DeferredReason = "no_rider_accepted"
)

type DeferredOrder struct {
	Order        order.Order
	Reason       DeferredReason 
	DeferredAtNs int64          // 从本次 Pipeline 启动到进入 Deferred 的相对时间，单位纳秒
	Attempt      uint32         // 当前是第几轮尝试
}

type DeferredOrderSink interface {
	Store(ctx context.Context, deferOrder DeferredOrder) error
}

type OrderSource interface {
	Next() (order.Order, bool, error)
}
