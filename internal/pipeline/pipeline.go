// Package pipeline implements the bounded CSP worker pipeline for strategy A.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"ride-sharing/internal/model"
	"ride-sharing/internal/report"
)

// 整个匹配系统的并发执行引擎
// 负责用 goroutine + channel 串起来，并尽可能高效地跑完所有订单
// 题目要求的 Go 协程 + CSP 并发模型最核心的一层

// pipeline 不关心具体实现
type OrderSource interface {
	Next() (model.Order, bool, error)
}

type Matcher interface {
	Match(model.Order) (model.Assignment, error)
}

// 控制整个并发流水线
type Options struct {
	Workers         int  // worker goroutine 数量
	BatchSize       int  // 一次通过 channel 传多少订单
	ChannelCapacity int  // 限制 channel 最多缓存多少个 batch (一个 bounded channel)
	ExpectedOrders  uint64
	PreviewSize     int
}

// 启动之前检查参数
func (o Options) Validate() error {
	var errs []error
	if o.Workers <= 0 {
		errs = append(errs, errors.New("workers must be greater than zero"))
	}
	if o.BatchSize <= 0 {
		errs = append(errs, errors.New("batch size must be greater than zero"))
	}
	if o.ChannelCapacity <= 0 {
		errs = append(errs, errors.New("channel capacity must be greater than zero"))
	}
	if o.ExpectedOrders == 0 {
		errs = append(errs, errors.New("expected orders must be greater than zero"))
	}
	if o.PreviewSize < 0 {
		errs = append(errs, errors.New("preview size cannot be negative"))
	}
	return errors.Join(errs...)
}

type Result struct {
	GeneratedOrders      uint64             `json:"generatedOrders"`
	AdmittedOrders       uint64             `json:"admittedOrders"`
	CompletedOrders      uint64             `json:"completedOrders"`
	UnfinishedOrders     uint64             `json:"unfinishedOrders"`
	LastPlannedArrivalNs int64              `json:"lastPlannedArrivalNs"`
	MaxQueueDepth        int                `json:"maxQueueDepthBatches"`
	Performance          PerformanceMetrics `json:"performance"`
	StrategyDetails      *StrategyMetrics   `json:"strategyDetails,omitempty"`
	WorkerCompleted      []uint64           `json:"workerCompletedOrders"`
	OrderPreview         []model.Order      `json:"-"`
	AssignmentPreview    []model.Assignment `json:"-"`
	Report               report.Summary     `json:"report"`
}

type StrategyMetrics struct {
	TopK                     int      `json:"topK"`
	MaxExtraDistanceMeters   float64  `json:"maxExtraDistanceMeters"`
	ReorderWindow            int      `json:"reorderWindow"`
	MaxCandidateQueueDepth   int      `json:"maxCandidateQueueDepth"`
	MaxReorderDepth          int      `json:"maxReorderDepth"`
	CandidateWorkerCompleted []uint64 `json:"candidateWorkerCompletedOrders"`
}

type PerformanceMetrics struct {
	PlannedArrivalWindowNs int64          `json:"plannedArrivalWindowNs"`
	ActualInjectionNs      int64          `json:"actualInjectionNs"`
	InjectionOverrunNs     int64          `json:"injectionOverrunNs"`
	DrainAfterWindowNs     int64          `json:"drainAfterWindowNs"`
	TotalRunNs             int64          `json:"totalRunNs"`
	ActualAdmissionRate    float64        `json:"actualAdmissionRatePerSecond"`
	ActualCompletionRate   float64        `json:"actualCompletionRatePerSecond"`
	AdmissionDelay         LatencySummary `json:"admissionDelay"`
	QueueAndMatchLatency   LatencySummary `json:"queueAndMatchLatency"`
	EndToEndLatency        LatencySummary `json:"endToEndLatency"`
}

type RunError struct {
	Cause     error
	Generated uint64
	Admitted  uint64
	Completed uint64
	Expected  uint64
}

func (e *RunError) Error() string {
	return fmt.Sprintf(
		"pipeline failed: %v (generated=%d admitted=%d completed=%d unfinished=%d)",
		e.Cause,
		e.Generated,
		e.Admitted,
		e.Completed,
		unfinished(e.Expected, e.Completed),
	)
}

func (e *RunError) Unwrap() error { return e.Cause }

type PanicError struct {
	WorkerID  int
	OrderID   uint64
	Sequence  uint64
	Recovered any
	Stack     string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf(
		"worker %d panicked while matching order ID %d sequence %d: %v\n%s",
		e.WorkerID,
		e.OrderID,
		e.Sequence,
		e.Recovered,
		e.Stack,
	)
}

type producerPanicError struct {
	Recovered any
	Stack     string
}

func (e *producerPanicError) Error() string {
	return fmt.Sprintf("order producer panicked: %v\n%s", e.Recovered, e.Stack)
}

type producerResult struct {
	generated            uint64
	admitted             uint64
	lastPlannedArrivalNs int64
	maxQueueDepth        int
	lastAdmissionNs      int64
	admissionLatency     latencyHistogram
	preview              []model.Order
	err                  error
}

type workerResult struct {
	workerID             int
	completed            uint64
	preview              []model.Assignment
	reporter             *report.Accumulator
	lastCompletionNs     int64
	queueAndMatchLatency latencyHistogram
	endToEndLatency      latencyHistogram
	err                  error
}

type admittedBatch struct {
	orders     []model.Order
	admittedNs atomic.Int64  // 保证 producer 和 worker 对这个值的并发访问是安全的
}

// 整个 pipeline 的总指挥
func Run(ctx context.Context, source OrderSource, matcher Matcher, riders []model.Rider, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("context cannot be nil")
	}
	if source == nil {
		return Result{}, errors.New("order source cannot be nil")
	}
	if matcher == nil {
		return Result{}, errors.New("matcher cannot be nil")
	}
	if err := options.Validate(); err != nil {
		return Result{}, err
	}

	// 先创建统计器
	mergedReporter, err := report.New(riders)
	if err != nil {
		return Result{}, fmt.Errorf("create pipeline reporter: %w", err)
	}
	// 不是让所有 worker 同时修改：同一个 reporter
	// 避免 hot path 上 多个 worker 抢同一个锁
	// 属于：thread-local / worker-local accumulation，最后 reduce/merge
	workerReporters := make([]*report.Accumulator, options.Workers)
	for index := range workerReporters {
		workerReporters[index] = mergedReporter.Fork()
	}

	// 任何一个地方失败，全部停
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	runStarted := time.Now()

	// batchChannel：真正的 CSP 通道
	// CSP 的核心思想就是：goroutine 尽量通过 channel 传递数据/所有权，
	// 而不是所有 goroutine 随意共享可变状态
	batchChannel := make(chan *admittedBatch, options.ChannelCapacity)
	
	bufferPool := make(chan *admittedBatch, options.ChannelCapacity+options.Workers)
	for range cap(bufferPool) {
		// 提前创建 batch
		bufferPool <- &admittedBatch{orders: make([]model.Order, 0, options.BatchSize)}
	}
	producerResults := make(chan producerResult, 1)
	workerResults := make(chan workerResult, options.Workers)

	// 启动 producer goroutine
	// producer 专门：
	// 生成订单
	// 等待计划到达时间
	// 组 batch
	// 发送 batch
	go produce(runContext, cancel, runStarted, source, batchChannel, bufferPool, options, producerResults)

	// 启动 worker pool
	var workers sync.WaitGroup
	workers.Add(options.Workers)

	for workerID := 0; workerID < options.Workers; workerID++ {
		go func() {
			defer workers.Done()
			work(runContext, cancel, runStarted, workerID, matcher, batchChannel, bufferPool, workerReporters[workerID], options.PreviewSize, workerResults)
		}()
	}

	// producer 结束了，再确保所有 worker 都结束，然后关闭 workerResults
	producerState := <-producerResults
	workers.Wait()
	close(workerResults)

	states := make([]workerResult, 0, options.Workers)
	for state := range workerResults {
		states = append(states, state)
	}
	sort.Slice(states, func(first, second int) bool {
		return states[first].workerID < states[second].workerID
	})

	result := Result{
		GeneratedOrders:      producerState.generated,
		AdmittedOrders:       producerState.admitted,
		LastPlannedArrivalNs: producerState.lastPlannedArrivalNs,
		MaxQueueDepth:        producerState.maxQueueDepth,
		WorkerCompleted:      make([]uint64, options.Workers),
		OrderPreview:         append([]model.Order(nil), producerState.preview...),
	}
	queueAndMatchLatency := latencyHistogram{}
	endToEndLatency := latencyHistogram{}
	lastCompletionNs := int64(0)
	var workerError error
	for _, state := range states {
		result.CompletedOrders += state.completed
		result.WorkerCompleted[state.workerID] = state.completed
		result.AssignmentPreview = append(result.AssignmentPreview, state.preview...)
		queueAndMatchLatency.merge(&state.queueAndMatchLatency)
		endToEndLatency.merge(&state.endToEndLatency)
		if state.lastCompletionNs > lastCompletionNs {
			lastCompletionNs = state.lastCompletionNs
		}
		if err := mergedReporter.Merge(state.reporter); err != nil && workerError == nil {
			workerError = fmt.Errorf("merge worker %d report: %w", state.workerID, err)
		}
		if state.err != nil && workerError == nil {
			workerError = state.err
		}
	}
	sort.Slice(result.OrderPreview, func(first, second int) bool {
		return result.OrderPreview[first].Sequence < result.OrderPreview[second].Sequence
	})
	sort.Slice(result.AssignmentPreview, func(first, second int) bool {
		return result.AssignmentPreview[first].Sequence < result.AssignmentPreview[second].Sequence
	})
	result.Report = mergedReporter.Summary()
	result.UnfinishedOrders = unfinished(options.ExpectedOrders, result.CompletedOrders)
	result.Performance = buildPerformanceMetrics(producerState, lastCompletionNs, queueAndMatchLatency, endToEndLatency)

	cause := workerError
	if cause == nil {
		cause = producerState.err
	}

	// 检查订单守恒
	// order-count conservation（订单数量守恒）
	// 并发 bug 最麻烦的一类就是：
	// 重复处理
	// 漏处理
	// 提前退出
	if cause == nil && (result.GeneratedOrders != options.ExpectedOrders ||
		result.AdmittedOrders != options.ExpectedOrders ||
		result.CompletedOrders != options.ExpectedOrders ||
		result.Report.AssignmentCount != options.ExpectedOrders ||
		result.Report.RiderOrderCountSum != options.ExpectedOrders) {
		cause = errors.New("normal completion violated order-count conservation")
	}
	if cause != nil {
		return result, &RunError{
			Cause:     cause,
			Generated: result.GeneratedOrders,
			Admitted:  result.AdmittedOrders,
			Completed: result.CompletedOrders,
			Expected:  options.ExpectedOrders,
		}
	}
	return result, nil
}

// 生产者
func produce(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	source OrderSource,
	batches chan<- *admittedBatch,
	pool chan *admittedBatch,
	options Options,
	results chan<- producerResult,
) {
	state := producerResult{preview: make([]model.Order, 0, options.PreviewSize)}
	var ownedBatch *admittedBatch
	arrivalTimer := time.NewTimer(time.Hour)
	if !arrivalTimer.Stop() {
		<-arrivalTimer.C
	}

	// Panic recovery：如果matcher意外，通知整个 pipeline 停止
	defer func() {
		arrivalTimer.Stop()
		if recovered := recover(); recovered != nil {
			state.err = &producerPanicError{Recovered: recovered, Stack: string(debug.Stack())}
			cancel()
		}
		if ownedBatch != nil {
			recycleBatch(pool, ownedBatch)
		}
		close(batches)
		results <- state
	}()

	for {
		select {
		case <-ctx.Done():
			state.err = ctx.Err()
			return
		case ownedBatch = <-pool:
			// producer 先拿一个空 batch
			// 下一批订单可以继续复用原来的内存
			ownedBatch.orders = ownedBatch.orders[:0]
			ownedBatch.admittedNs.Store(0)
		}

		sourceEnded := false

		// producer 不断 source.Next()
		for len(ownedBatch.orders) < options.BatchSize {
			order, ok, err := source.Next()
			if err != nil {
				state.err = fmt.Errorf("generate order: %w", err)
				cancel()
				return
			}
			if !ok {
				sourceEnded = true
				break
			}
			state.generated++  // source.Next() 已经生成出来
			state.lastPlannedArrivalNs = order.PlannedArrivalNs
			if uint64(len(state.preview)) < uint64(options.PreviewSize) {
				state.preview = append(state.preview, order)
			}
			if err := waitForPlannedArrival(ctx, start, order.PlannedArrivalNs, arrivalTimer); err != nil {
				state.err = err
				return
			}
			ownedBatch.orders = append(ownedBatch.orders, order)
		}

		if len(ownedBatch.orders) != 0 {
			batchSize := len(ownedBatch.orders)
			select {
			case batches <- ownedBatch:
				// batch 满了以后发送
				admittedNs := time.Since(start).Nanoseconds()
				ownedBatch.admittedNs.Store(admittedNs + 1)
				for _, order := range ownedBatch.orders {
					state.admissionLatency.observe(admittedNs - order.PlannedArrivalNs)
				}

				// producer 已经把这个 batch 的所有权交给 worker 了, producer 不应该再修改它
				ownedBatch = nil
				state.admitted += uint64(batchSize) // 已经成功进入 worker pipeline
				state.lastAdmissionNs = admittedNs
				if queueDepth := len(batches); queueDepth > state.maxQueueDepth {
					state.maxQueueDepth = queueDepth
				}
			case <-ctx.Done():
				state.err = ctx.Err()
				return
			}
		}
		if sourceEnded {
			return
		}
	}
}

// 消费者
func work(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	workerID int,
	matcher Matcher,
	batches <-chan *admittedBatch,
	pool chan *admittedBatch,
	reporter *report.Accumulator,
	previewSize int,
	results chan<- workerResult,
) {
	state := workerResult{
		workerID: workerID,
		preview:  make([]model.Assignment, 0, previewSize),
		reporter: reporter,
	}
	var currentOrder model.Order
	var hasCurrentOrder bool
	var ownedBatch *admittedBatch
	var admittedNs int64
	defer func() {
		if recovered := recover(); recovered != nil {
			panicError := &PanicError{
				WorkerID:  workerID,
				OrderID:   currentOrder.ID,
				Sequence:  currentOrder.Sequence,
				Recovered: recovered,
				Stack:     string(debug.Stack()),
			}
			if !hasCurrentOrder {
				panicError.OrderID = 0
				panicError.Sequence = 0
			}
			state.err = panicError
			cancel()
		}
		if ownedBatch != nil {
			recycleBatch(pool, ownedBatch)
		}
		results <- state
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-batches:
			// 从 channel 取 batch
			if !ok {
				return
			}
			ownedBatch = batch  // worker 现在拥有这个 batch
			for admittedNs = batch.admittedNs.Load(); admittedNs == 0; admittedNs = batch.admittedNs.Load() {
				runtime.Gosched() // 现在先让其他 goroutine 有机会运行
			}
			admittedNs--
		}

		// worker 对 batch 中每个订单执行 Match
		for index := range ownedBatch.orders {
			select {
			case <-ctx.Done():
				return
			default:
			}
			currentOrder = ownedBatch.orders[index]
			hasCurrentOrder = true
			// 注意：pipeline 本身没有最近骑手搜索算法
			assignment, err := matcher.Match(currentOrder)
			if err != nil {
				state.err = fmt.Errorf(
					"worker %d match order ID %d sequence %d: %w",
					workerID,
					currentOrder.ID,
					currentOrder.Sequence,
					err,
				)
				cancel()
				return
			}

			// 匹配之后立即统计
			if err := reporter.Observe(assignment); err != nil {
				state.err = fmt.Errorf(
					"worker %d record order ID %d sequence %d: %w",
					workerID,
					currentOrder.ID,
					currentOrder.Sequence,
					err,
				)
				cancel()
				return
			}
			state.completed++
			completedAt := time.Now()
			state.lastCompletionNs = completedAt.Sub(start).Nanoseconds()
			state.queueAndMatchLatency.observe(state.lastCompletionNs - admittedNs)
			state.endToEndLatency.observe(completedAt.Sub(start.Add(time.Duration(currentOrder.PlannedArrivalNs))).Nanoseconds())
			if assignment.Sequence < uint64(previewSize) {
				state.preview = append(state.preview, assignment)
			}
			hasCurrentOrder = false
		}

		// worker 完成 batch 后归还内存
		recycleBatch(pool, ownedBatch)
		ownedBatch = nil
	}
}

func buildPerformanceMetrics(
	producer producerResult,
	lastCompletionNs int64,
	queueAndMatchLatency latencyHistogram,
	endToEndLatency latencyHistogram,
) PerformanceMetrics {
	injectionOverrunNs := producer.lastAdmissionNs - producer.lastPlannedArrivalNs
	if injectionOverrunNs < 0 {
		injectionOverrunNs = 0
	}
	drainAfterWindowNs := lastCompletionNs - producer.lastPlannedArrivalNs
	if drainAfterWindowNs < 0 {
		drainAfterWindowNs = 0
	}
	metrics := PerformanceMetrics{
		PlannedArrivalWindowNs: producer.lastPlannedArrivalNs,
		ActualInjectionNs:      producer.lastAdmissionNs,
		InjectionOverrunNs:     injectionOverrunNs,
		DrainAfterWindowNs:     drainAfterWindowNs,
		TotalRunNs:             lastCompletionNs,
		AdmissionDelay:         producer.admissionLatency.summary(),
		QueueAndMatchLatency:   queueAndMatchLatency.summary(),
		EndToEndLatency:        endToEndLatency.summary(),
	}
	if producer.lastAdmissionNs > 0 {
		metrics.ActualAdmissionRate = float64(producer.admitted) / (float64(producer.lastAdmissionNs) / float64(time.Second))
	}
	if lastCompletionNs > 0 {
		metrics.ActualCompletionRate = float64(endToEndLatency.count) / (float64(lastCompletionNs) / float64(time.Second))
	}
	return metrics
}

// 模拟 订单在 arrival window 内逐渐/突发到达
func waitForPlannedArrival(ctx context.Context, start time.Time, plannedArrivalNs int64, timer *time.Timer) error {
	if plannedArrivalNs <= 0 {
		return nil
	}
	wait := time.Until(start.Add(time.Duration(plannedArrivalNs)))
	if wait <= 0 {
		return nil
	}
	timer.Reset(wait)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		return ctx.Err()
	}
}

func recycleBatch(pool chan *admittedBatch, batch *admittedBatch) {
	select {
	case pool <- batch:
	default:
	}
}

// 未完成订单
func unfinished(expected, completed uint64) uint64 {
	if completed >= expected {
		return 0
	}
	return expected - completed
}
