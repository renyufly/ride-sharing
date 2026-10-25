package memory

import (
	"context"
	"fmt"
	appmatching "ride-sharing/internal/app/matching"
	"sync"
)

type MemorySink struct {
	mu     sync.Mutex
	limit  int
	total  uint64
	orders []appmatching.DeferredOrder
}

type MemorySnapshot struct {
	Total  uint64
	Orders []appmatching.DeferredOrder
}

func NewMemorySink(limit int) (*MemorySink, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must greater than 0")
	}

	memorySink := MemorySink{
		limit: limit,
	}

	if limit > 0 {
		orders := make([]appmatching.DeferredOrder, 0, limit)
		memorySink.orders = orders
	}

	return &memorySink, nil

}

func (s *MemorySink) Store(
	ctx context.Context,
	order appmatching.DeferredOrder,
) error {
	if s == nil {
		return fmt.Errorf("Memory sink is nil!")
	}

	if ctx == nil {
		return fmt.Errorf("context is nil")
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.total++

	if len(s.orders) < s.limit {
		s.orders = append(s.orders, order)
	}

	return nil

}

func (s *MemorySink) Snapshot() MemorySnapshot {
	if s == nil {
		return MemorySnapshot{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	total := s.total
	orders := make([]appmatching.DeferredOrder, len(s.orders))
	copy(orders, s.orders)

	return MemorySnapshot{
		Total:  total,
		Orders: orders,
	}

}
