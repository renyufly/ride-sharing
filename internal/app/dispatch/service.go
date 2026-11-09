package dispatch

import (
	"ride-sharing/internal/domain/matching"
	"sync"
)

// 一条等待回复的接单请求
type pendingRequest struct {
	orderID  uint64
	riderUID uint64
	reply    chan bool // true=接单，false=拒单
}

// 管理所有等待中的接单请求
type Service struct {
	mu      sync.Mutex
	pending map[string]pendingRequest // requestID → pendingRequest
	dispatchMu sync.Mutex 
	loads      map[uint64]uint64   // riderUID -> orderCount
	assigned   map[uint64]matching.Assignment  // riderUID -> assignment
}

func NewService() *Service{
	return &Service{
		pending: make(map[string]pendingRequest),
		loads: make(map[uint64]uint64),
		assigned: make(map[uint64]matching.Assignment),
	}
}

// 添加 新的等待中的接单请求
func (s *Service) register(orderID, riderUID uint64, requestID string) <-chan bool {
	reply := make(chan bool, 1)
	s.mu.Lock()
	s.pending[requestID] = pendingRequest{
		orderID: orderID,
		riderUID: riderUID,
		reply: reply,
	}
	s.mu.Unlock()

	return reply
}

// 删除等待中的接单请求. 返回true表示成功删除
func (s *Service) expire(requestID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pending[requestID]; !ok {
		return false
	}
	
	delete(s.pending, requestID)
	return true
}

// 接收当前有效请求。返回 false 表示接收失败
func (s *Service) Respond(orderID, riderUID uint64, requestID string, accepted bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 根据requestID取出对应接单请求
	request, ok := s.pending[requestID]

	if !ok || request.orderID != orderID || request.riderUID != riderUID {
		return false
	}

	delete(s.pending, requestID)

	request.reply <- accepted

	return true
}