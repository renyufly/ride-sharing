package pipeline

import (
	"ride-sharing/internal/model"
	"context"
)

// 定义“未分配订单的数据契约”
type DeferredReason string

const (
	// 在距离和 Top-K 条件内，没有未达到额度的骑手
	DeferredReasonCapacityExhausted DeferredReason="capacity_exhausted"
	// 轮到 Coordinator 决策时，已经超过主分配窗口
	DeferredReasonWindowExpired DeferredReason="window_expired"
)

type DeferredOrder struct {
	Order         model.Order
    Reason        DeferredReason  // 为什么没有分配的原因
    DeferredAtNs  int64  // 从本次 Pipeline 启动到进入 Deferred 的相对时间，单位纳秒
	Attempt 	  uint32   // 当前是第几轮尝试
}

type DeferredOrderSink interface {
	Store(ctx context.Context, deferOrder DeferredOrder) error
}