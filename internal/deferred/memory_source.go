package deferred

import (
	"errors"
	"fmt"
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
)

type MemorySource struct {
	orders []pipeline.DeferredOrder
	next int
}

func NewMemorySource(orders []pipeline.DeferredOrder) (*MemorySource, error) {
	if len(orders) == 0 {
		return nil, fmt.Errorf("empty")
	}

	copiedOrders := make([]pipeline.DeferredOrder, len(orders))

	// （浅拷贝切片元素，切断底层数组引用）
	copy(copiedOrders, orders)

	// 使用新切片初始化 MemorySource，next 保持默认值 0
	source := &MemorySource{
		orders: copiedOrders,
		next:   0,
	}

	return source, nil

}

func (s *MemorySource) Next() (model.Order, bool, error) {
	if s == nil {
		return model.Order{}, false, errors.New("memory source is nil")
	}

	// 如果 next >= orders 长度，说明已无更多订单，正常结束
	if s.next >= len(s.orders) {
		return model.Order{}, false, nil
	}

	// 读取 orders[next].Order 的值副本
	orderCopy := s.orders[s.next].Order

	orderCopy.Sequence = uint64(s.next)

	orderCopy.PlannedArrivalNs = 0

	s.next++

	return orderCopy, true, nil
}