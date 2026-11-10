package dispatch

import (
	"errors"
	"fmt"
	"sync"

	"ride-sharing/internal/domain/matching"
)

var (
	ErrOrderAlreadyAssigned = errors.New("order already assigned")
	ErrOrderInProgress      = errors.New("order dispatch in progress")
	ErrOrderNotInProgress   = errors.New("order dispatch not in progress")
	ErrOrderNotAssigned = errors.New("order not assigned")
	ErrOrderRiderMismatch = errors.New("order rider mismatch")
)

type RiderOrderStatus string

const (
    RiderOrderUnknown   RiderOrderStatus = "unknown"
    RiderOrderPending   RiderOrderStatus = "pending"
    RiderOrderAssigned  RiderOrderStatus = "assigned"
    RiderOrderCompleted RiderOrderStatus = "completed"
)

// LiveLedger 在同一服务进程的多次实时请求之间共享。
// 所有字段都由 mu 保护；网络等待期间不得持有 mu。
type LiveLedger struct {
	mu sync.Mutex

	assigned     map[uint64]matching.Assignment // orderID -> 最终分配
	inProgress   map[uint64]struct{}            // 正在派单的订单
	reservations map[uint64]uint64              // orderID -> 暂时预占的 riderUID
	loads        map[uint64]uint64              // riderUID -> 已接单数
	reserved     map[uint64]uint64              // riderUID -> 待回复数
	completed 	 map[uint64]struct{}            // 已完成的订单；防止重复释放容量

	changed 	 chan struct{}
}

func NewLiveLedger() *LiveLedger {
	return &LiveLedger{
		assigned:     make(map[uint64]matching.Assignment),
		inProgress:   make(map[uint64]struct{}),
		reservations: make(map[uint64]uint64),
		loads:        make(map[uint64]uint64),
		reserved:     make(map[uint64]uint64),
		completed: make(map[uint64]struct{}),
		changed: make(chan struct{}),
	}
}


// Begin 对一个订单取得进程内派单权。每个订单只调用一次；
// 拒单后换骑手时不重复调用。
func (l *LiveLedger) Begin(orderID uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.assigned[orderID]; exists {
		return ErrOrderAlreadyAssigned
	}
	if _, exists := l.inProgress[orderID]; exists {
		return ErrOrderInProgress
	}

	l.inProgress[orderID] = struct{}{}

	l.signalLocked()

	return nil
}

// TryReserve 原子检查容量并预占。
// 返回 (false, nil) 表示骑手当前没有空余容量。
func (l *LiveLedger) TryReserve(
	orderID, riderUID uint64,
	maxOrdersPerRider int,
) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.inProgress[orderID]; !exists {
		return false, ErrOrderNotInProgress
	}
	if _, exists := l.reservations[orderID]; exists {
		return false, fmt.Errorf("order %d already has a reservation", orderID)
	}

	load := l.loads[riderUID] + l.reserved[riderUID]
	if maxOrdersPerRider > 0 && load >= uint64(maxOrdersPerRider) {
		return false, nil
	}

	l.reservations[orderID] = riderUID
	l.reserved[riderUID]++

	l.signalLocked()

	return true, nil
}

// Release 在拒单、超时或发送失败后释放该次预占。
func (l *LiveLedger) Release(orderID, riderUID uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, exists := l.reservations[orderID]
	if !exists || current != riderUID || l.reserved[riderUID] == 0 {
		return fmt.Errorf(
			"order %d has no reservation for rider %d",
			orderID, riderUID,
		)
	}

	delete(l.reservations, orderID)
	l.reserved[riderUID]--

	l.signalLocked()

	return nil
}

// Commit 在骑手明确接单后调用。
// 同一次操作中释放预占、增加已接单量并记录最终分配。
func (l *LiveLedger) Commit(
	assignment matching.Assignment,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	orderID := assignment.OrderID
	riderUID := assignment.RiderUID

	if _, exists := l.inProgress[orderID]; !exists {
		return ErrOrderNotInProgress
	}
	current, exists := l.reservations[orderID]
	if !exists || current != riderUID || l.reserved[riderUID] == 0 {
		return fmt.Errorf(
			"order %d has no reservation for rider %d",
			orderID, riderUID,
		)
	}

	delete(l.reservations, orderID)
	l.reserved[riderUID]--
	l.loads[riderUID]++
	l.assigned[orderID] = assignment
	delete(l.inProgress, orderID)

	l.signalLocked()

	return nil
}

// Complete 释放已完成订单占用的骑手容量。(订单完成)
// 保留 assigned 记录，避免同一订单 ID 被再次派单。
func (l *LiveLedger) Complete(orderID, riderUID uint64) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    assignment, exists := l.assigned[orderID]
    if !exists {
        return fmt.Errorf("%w: order %d", ErrOrderNotAssigned, orderID)
    }
    if assignment.RiderUID != riderUID {
        return fmt.Errorf("%w: order %d", ErrOrderRiderMismatch, orderID)
    }
    if _, done := l.completed[orderID]; done {
        return nil 
    }
    if l.loads[riderUID] == 0 {
        return fmt.Errorf("order %d has no active rider load", orderID)
    }

    l.loads[riderUID]--
    l.completed[orderID] = struct{}{}
    l.signalLocked()
    return nil
}

// Abort 在订单最终 deferred 或整次运行取消时清理派单权。
// 允许重复调用。
func (l *LiveLedger) Abort(orderID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, inProgress := l.inProgress[orderID]
	changed := inProgress

	if riderUID, exists := l.reservations[orderID]; exists {
		delete(l.reservations, orderID)
		l.reserved[riderUID]--
		changed = true
	}
	delete(l.inProgress, orderID)

	if changed {
		l.signalLocked()
	}
}

// Counts 读取某骑手当前已完成及待回复的订单量。
func (l *LiveLedger) Counts(riderUID uint64) (load, reserved uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loads[riderUID], l.reserved[riderUID]
}

// signalLocked 关闭旧 channel , 唤醒所有等待账本变化的协调器。
func (l *LiveLedger) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// SnapshotInto 同时取得一组骑手的负载和下一次变化通知。
func (l *LiveLedger) SnapshotInto(
	riderUIDs []uint64,
	loads []uint64,
	reserved []uint64,
) (<-chan struct{}, error) {
	if len(riderUIDs) != len(loads) ||
		len(riderUIDs) != len(reserved) {
		return nil, fmt.Errorf("ledger snapshot slice lengths differ")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for i, riderUID := range riderUIDs {
		loads[i] = l.loads[riderUID]
		reserved[i] = l.reserved[riderUID]
	}
	return l.changed, nil
}

func (l *LiveLedger) StatusForRider(
    orderID, riderUID uint64,
) RiderOrderStatus {
    l.mu.Lock()
    defer l.mu.Unlock()

    if assignment, ok := l.assigned[orderID]; ok {
        if assignment.RiderUID != riderUID {
            return RiderOrderUnknown
        }
        if _, done := l.completed[orderID]; done {
            return RiderOrderCompleted
        }
        return RiderOrderAssigned
    }

    if reservedRider, ok := l.reservations[orderID]; ok &&
        reservedRider == riderUID {
        return RiderOrderPending
    }
    return RiderOrderUnknown
}