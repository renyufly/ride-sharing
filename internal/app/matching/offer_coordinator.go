package matching

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"time"

	"ride-sharing/internal/app/report"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	domainmatching "ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/rider"
)

type offerCoordinator struct {
    ctx          context.Context
    start        time.Time
    options      BalancedOptions

    resultPool   chan<- *candidateResult
    deferredSink DeferredOrderSink

    loads       []uint64
    reserved    []uint64
    uidIndex    map[uint64]int

    pending map[uint64]*candidateResult
    active  map[uint64]*pendingOffer
    queued  []offerJob
	waiting map[uint64]*pendingOffer    // 等待中的订单

	ledger OfferLedger

	riderUIDs []uint64
	changes   <-chan struct{}
	claimed   map[uint64]struct{}

    next  uint64
    state coordinatorResult
}

// recycle 将已不再使用的 candidateResult 归还给对象池
func (c *offerCoordinator) recycle(
    ready *candidateResult,
) error {
    ready.candidates = ready.candidates[:0]

    select {
    case c.resultPool <- ready:
        return nil
    case <-c.ctx.Done():
        return c.ctx.Err()
    }
}

// deferReady 将当前无法完成匹配的订单保存为 DeferredOrder
func (c *offerCoordinator) deferReady(
    ready *candidateResult,
    reason DeferredReason,
) error {
    deferred := DeferredOrder{
        Order:        ready.order,
        Reason:       reason,
        DeferredAtNs: time.Since(c.start).Nanoseconds(),
        Attempt:      c.options.Attempt,
    }

    if err := c.deferredSink.Store(c.ctx, deferred); err != nil {
        return err
    }

	if _, claimed := c.claimed[ready.order.ID]; claimed {
		c.ledger.Abort(ready.order.ID)
		delete(c.claimed, ready.order.ID)
	}

    c.state.deferred++

    switch reason {
    case DeferredReasonWindowExpired:
        c.state.deferredByWindow++
    case DeferredReasonCapacityExhausted:
        c.state.deferredByCapacity++
    }

    if len(c.state.deferredPreview) < c.options.PreviewSize {
        c.state.deferredPreview = append(
            c.state.deferredPreview,
            deferred,
        )
    }

    return c.recycle(ready)
}

// selectAndQueue 选择骑手并创建邀请
func (c *offerCoordinator) selectAndQueue(
	p *pendingOffer,
) (found bool, waitForReservation bool, err error) {
    decisionStarted := time.Now()
	firstAttempt := len(p.tried) == 0
	var capacityFiltered uint64

	observeDecision := func() {
		decisionNs := time.Since(decisionStarted).Nanoseconds()
		c.state.assignmentDecisionLatency.Observe(decisionNs)

		algorithmNs := decisionNs
		if firstAttempt {
			algorithmNs += p.ready.candidateSearchNs
		}
		c.state.algorithmComputeLatency.Observe(algorithmNs)
		c.state.capacityFiltered += capacityFiltered
	}

	for {
		if err := c.ctx.Err(); err != nil {
			return false, false, err
		}
		if err := c.refreshCounts(); err != nil {
			return false, false, err
		}

		selection, err := chooseOfferCandidate(
			p.ready,
			c.loads,
			c.reserved,
			c.uidIndex,
			p.tried,
			c.options.MaxExtraDistanceMeters,
			c.options.MaxOrdersPerRider,
		)
		if err != nil {
			return false, false, err
		}
		capacityFiltered += selection.capacityFiltered

		if !selection.assigned {
			observeDecision()
			return false, selection.blockedByReservation, nil
		}

		reserved, err := c.ledger.TryReserve(
			p.ready.order.ID,
			selection.assignment.RiderUID,
			c.options.MaxOrdersPerRider,
		)
		if err != nil {
			return false, false, err
		}
		if !reserved {
			continue
		}

		p.selection = selection
		if err := c.refreshCounts(); err != nil {
			_ = c.ledger.Release(
				p.ready.order.ID,
				selection.assignment.RiderUID,
			)
			return false, false, err
		}

		c.queued = append(c.queued, p.makeOfferJob())
		observeDecision()
		return true, false, nil
	}
}

func (c *offerCoordinator) refreshCounts() error {
	changes, err := c.ledger.SnapshotInto(
		c.riderUIDs, c.loads, c.reserved,
	)
	if err != nil {
		return err
	}
	c.changes = changes
	return nil
}

// retryWaiting 按订单顺序重试等待队列 waiting
func (c *offerCoordinator) retryWaiting() error {
	sequences := make([]uint64, 0, len(c.waiting))

	for sequence := range c.waiting {
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(i, j int) bool {
		return sequences[i] < sequences[j]
	})

	for _, sequence := range sequences {
		p := c.waiting[sequence]

		found, blocked, err := c.selectAndQueue(p)
		if err != nil {
			return err
		}
		if found {
			delete(c.waiting, sequence)
			c.active[sequence] = p
			continue
		}
		if blocked {
			continue
		}

		delete(c.waiting, sequence)
		reason := DeferredReasonCapacityExhausted
		if len(p.tried) > 0 {
			reason = DeferredReasonNoRiderAccepted
		}
		if err := c.deferReady(p.ready, reason); err != nil {
			return err
		}
	}
	return nil
}


func coordinateBalancedOffers(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	candidates <-chan *candidateResult,
	resultPool chan *candidateResult,
	riders []rider.Rider,
	options BalancedOptions,
	jobs chan<- offerJob,
	offerResults <-chan offerResult,
	output chan<- coordinatorResult,
	deferredSink DeferredOrderSink,
	ledger OfferLedger,
) {
	// 创建报告统计器
	reporter, err := report.New(riders)
	state := coordinatorResult{
		preview:         make([]domainmatching.Assignment, 0, options.PreviewSize),
		deferredPreview: make([]DeferredOrder, 0, options.PreviewSize),
		reporter:        reporter,
	}
	if err != nil {
		state.err = err
		cancel()
		output <- state
		return
	}

	c := &offerCoordinator{
		ctx:          ctx,
		start:        start,
		options:      options,
		resultPool:   resultPool,
		deferredSink: deferredSink,

		loads:    make([]uint64, len(riders)),
		reserved: make([]uint64, len(riders)),
		uidIndex: make(map[uint64]int, len(riders)),
		// pending：候选骑手搜索完成、尚未轮到选择的订单。
		pending: make(map[uint64]*candidateResult, options.ReorderWindow()),
		// active：已选出骑手、正在排队或等待回复的订单。
		active:  make(map[uint64]*pendingOffer),
		// 待发送邀请队列
		queued:  make([]offerJob, 0, options.ReorderWindow()),
		waiting: make(map[uint64]*pendingOffer),

		ledger: ledger,

		riderUIDs: make([]uint64, len(riders)),
		claimed:   make(map[uint64]struct{}),

		state: state,
	}
	for index, currentRider := range riders {
		c.uidIndex[currentRider.UID] = index
		c.riderUIDs[index] = currentRider.UID
	}

	// 统一处理异常和最终结果
	defer func() {
		if recovered := recover(); recovered != nil {
			c.state.err = fmt.Errorf(
				"offer coordinator panicked: %v\n%s",
				recovered,
				debug.Stack(),
			)
			cancel()
		}

		for orderID := range c.claimed {
			c.ledger.Abort(orderID)
		}

		output <- c.state
	}()
	

	for {
		// 候选输入结束后，仍要等所有已派出的邀请得到结果。
		if candidates == nil {
			if len(c.pending) != 0 {
				c.state.err = fmt.Errorf("candidate stream ended with %d orders out of sequence", len(c.pending))
				cancel()
				return
			}
			if len(c.active) == 0 && len(c.queued) == 0 && len(c.waiting) == 0 {

				if c.next != options.ExpectedOrders {
					c.state.err = fmt.Errorf(
						"processed %d orders; expected %d",
						c.next,
						options.ExpectedOrders,
					)
				}
				return
			}
		}

		// 没有待发送任务时，nil channel 会禁用 select 的发送分支。
		var jobOutput chan<- offerJob
		var nextJob offerJob
		if len(c.queued) > 0 {
			jobOutput = jobs
			nextJob = c.queued[0]
		}

		select {
		case <-ctx.Done():
			c.state.err = ctx.Err()
			return

		case jobOutput <- nextJob:
			// 向 Worker 发送下一条邀请
			c.queued = c.queued[1:]

		case candidate, ok := <-candidates:
			// 接收新的候选骑手搜索结果
			if !ok {
				candidates = nil
				continue
			}

			sequence := candidate.order.Sequence
			if sequence >= options.ExpectedOrders || sequence < c.next {
				c.state.err = fmt.Errorf("invalid or duplicate candidate sequence %d", sequence)
				cancel()
				return
			}
			if _, duplicate := c.pending[sequence]; duplicate {
				c.state.err = fmt.Errorf("duplicate candidate sequence %d", sequence)
				cancel()
				return
			}

			c.pending[sequence] = candidate
			if len(c.pending) > c.state.maxReorderDepth {
				c.state.maxReorderDepth = len(c.pending)
			}

			for {
				// 按序取出订单
				ready, exists := c.pending[c.next]
				if !exists {
					break
				}
				delete(c.pending, c.next)
				c.next++

				c.state.candidateSearchLatency.Observe(ready.candidateSearchNs)

				if options.AssignmentWindow > 0 &&
					time.Since(start) >= options.AssignmentWindow {
					if err := c.deferReady(ready, DeferredReasonWindowExpired); err != nil {
						c.state.err = err
						cancel()
						return
					}
					continue
				}

				if err := c.ledger.Begin(ready.order.ID); err != nil {
					c.state.err = err
					cancel()
					return
				}
				c.claimed[ready.order.ID] = struct{}{}

				p := &pendingOffer{
					ready: ready,
					tried: make(map[uint64]struct{}, len(ready.candidates)),
				}
				// 选择骑手并预占
				found, blocked, err := c.selectAndQueue(p)
				if err != nil {
					c.state.err = err
					cancel()
					return
				}
				if !found {
					if blocked {
						c.waiting[ready.order.Sequence] = p
						continue
					}
					if err := c.deferReady(
						ready, DeferredReasonCapacityExhausted,
					); err != nil {
						c.state.err = err
						cancel()
						return
					}

					continue
				}
				c.active[ready.order.Sequence] = p
			}

		case result, ok := <-offerResults:
			// 接收骑手邀请结果
			if !ok {
				c.state.err = fmt.Errorf("offer workers stopped before coordinator finished")
				cancel()
				return
			}

			sequence := result.job.order.Sequence
			p, exists := c.active[sequence]
			if !exists {
				c.state.err = fmt.Errorf("unexpected offer result for sequence %d", sequence)
				cancel()
				return
			}
			if err := p.validateOfferResult(result); err != nil {
				c.state.err = err
				cancel()
				return
			}
			if err := result.err; err != nil {
				c.state.err = err
				cancel()
				return
			}

			if result.attempt.RequestID != "" {
				c.state.attempts = append(c.state.attempts, result.attempt)
			}

			switch result.attempt.Status {
			case domaindispatch.StatusAccepted:
				if err := c.ledger.Commit(p.selection.assignment); err != nil {
					c.state.err = err
					cancel()
					return
				}
				close(result.committed)
				delete(c.claimed, p.ready.order.ID)

				if err := commitBalancedAssignment(
					start, p.ready, p.selection, c.loads, &c.state, options,
				); err != nil {
					c.state.err = err
					cancel()
					return
				}
				delete(c.active, sequence)
				if err := c.recycle(p.ready); err != nil {
					c.state.err = err
					cancel()
					return
				}

			case domaindispatch.StatusDeclined,
				domaindispatch.StatusTimedOut,
				domaindispatch.StatusDeliveryFailed:
				// 拒单、超时、发送失败分支
				if err := c.ledger.Release(
					p.ready.order.ID,
					p.selection.assignment.RiderUID,
				); err != nil {
					c.state.err = err
					cancel()
					return
				}
				delete(c.active, sequence)
				c.waiting[sequence] = p

			default:
				c.state.err = fmt.Errorf(
					"unexpected offer status %q for order %d",
					result.attempt.Status,
					result.job.order.ID,
				)
				cancel()
				return
			}


			if err := c.retryWaiting(); err != nil {
				c.state.err = err
				cancel()
				return
			}

			case <-c.changes:
				if err := c.refreshCounts(); err != nil {
					c.state.err = err
					cancel()
					return
				}
				if err := c.retryWaiting(); err != nil {
					c.state.err = err
					cancel()
					return
				}
		}
	}
}

