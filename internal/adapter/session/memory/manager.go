package memory

import (
	"fmt"
	"sync"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/platform/config"
)

// State is the last run kept by one process.
type State[Round any] struct {
	RunID          string
	CurrentRound   int
	Config         config.Config
	PreviewSize    int
	DeferredOrders []appmatching.DeferredOrder
	Rounds         []Round
}

// RetryStatus describes why the last run cannot be replayed.
type RetryStatus uint8

const (
	RetryReady RetryStatus = iota
	RetrySessionMissing
	RetryRunReplaced
	RetryRoundStale
	RetryNoDeferredOrders
	RetryRequiresBalanced
)

// Manager serializes runs and keeps only the latest run in this process.
type Manager[Round any] struct {
	slot      chan struct{}
	nextRunID uint64
	last      State[Round]
	hasLast   bool
}

func New[Round any]() *Manager[Round] {
	return &Manager[Round]{slot: make(chan struct{}, 1)}
}

// Lease gives one request exclusive access until End is called.
type Lease[Round any] struct {
	manager *Manager[Round]
	once    sync.Once
}

func (m *Manager[Round]) TryBegin() (*Lease[Round], bool) {
	select {
	case m.slot <- struct{}{}:
		return &Lease[Round]{manager: m}, true
	default:
		return nil, false
	}
}

func (l *Lease[Round]) End() {
	l.once.Do(func() { <-l.manager.slot })
}

func (l *Lease[Round]) SaveFirst(cfg config.Config, previewSize int, deferred []appmatching.DeferredOrder, round Round) State[Round] {
	m := l.manager
	m.nextRunID++
	m.last = State[Round]{
		RunID: fmt.Sprintf("run-%d", m.nextRunID), CurrentRound: 1,
		Config: cfg, PreviewSize: previewSize,
		DeferredOrders: append([]appmatching.DeferredOrder(nil), deferred...),
		Rounds:         []Round{round},
	}
	m.hasLast = true
	return clone(m.last)
}

func (l *Lease[Round]) RetryState(runID string, sourceRound int) (State[Round], RetryStatus) {
	m := l.manager
	if !m.hasLast {
		return State[Round]{}, RetrySessionMissing
	}
	if runID != m.last.RunID {
		return State[Round]{}, RetryRunReplaced
	}
	if sourceRound != m.last.CurrentRound {
		return State[Round]{}, RetryRoundStale
	}
	if len(m.last.DeferredOrders) == 0 {
		return State[Round]{}, RetryNoDeferredOrders
	}
	if m.last.Config.Strategy != config.StrategyBalanced {
		return State[Round]{}, RetryRequiresBalanced
	}
	return clone(m.last), RetryReady
}

func (l *Lease[Round]) SaveNext(cfg config.Config, deferred []appmatching.DeferredOrder, round Round) State[Round] {
	m := l.manager
	m.last.Config = cfg
	m.last.CurrentRound++
	m.last.DeferredOrders = append([]appmatching.DeferredOrder(nil), deferred...)
	m.last.Rounds = append(m.last.Rounds, round)
	return clone(m.last)
}

func clone[Round any](state State[Round]) State[Round] {
	state.DeferredOrders = append([]appmatching.DeferredOrder(nil), state.DeferredOrders...)
	state.Rounds = append([]Round(nil), state.Rounds...)
	return state
}
