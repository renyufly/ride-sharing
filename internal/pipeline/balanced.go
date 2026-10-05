package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/model"
	"ride-sharing/internal/report"
)

// Strategy B：在“距离尽量近”的前提下，兼顾骑手订单量均衡
// 先找到订单附近的 Top-K 个候选骑手，再从“距离可以接受”的候选骑手里优先选择当前订单量较少的骑手
/*
不能让 Worker 直接完成分配：1.数据竞争 2.加锁也结果不确定
所以：并行计算候选人，串行完成最终决策
*/

// 给我一个订单，找出距离最近的 K 个骑手
type CandidateMatcher interface {
	TopKInto(model.Order, int, []matchrule.Candidate) ([]matchrule.Candidate, error)
}

type BalancedOptions struct {
	Options
	TopK                   int  // 每个订单先找最近的 K 个骑手
	MaxExtraDistanceMeters float64  // 允许参与负载均衡的最远距离
	MaxOrdersPerRider      int  // 每个骑手的订单上限
	AssignmentWindow 	   time.Duration  // 主分配阶段时长 == 订单总生成窗口arrival window
	Attempt 			   uint32
}

func (o BalancedOptions) Validate(riderCount int) error {
	var errs []error
	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}
	if o.TopK <= 0 {
		errs = append(errs, errors.New("top-k must be greater than zero"))
	}
	if o.TopK > riderCount {
		errs = append(errs, errors.New("top-k cannot exceed rider count"))
	}
	if o.MaxExtraDistanceMeters < 0 || math.IsNaN(o.MaxExtraDistanceMeters) || math.IsInf(o.MaxExtraDistanceMeters, 0) {
		errs = append(errs, errors.New("max extra distance must be finite and non-negative"))
	}

	if o.Attempt <= 0 {
		errs = append(errs, errors.New("attempt must be greater than 0"))
	}
	return errors.Join(errs...)
}

// 暂存并发的乱序结果
func (o BalancedOptions) ReorderWindow() int {
	return o.ChannelCapacity*o.BatchSize + o.Workers*o.BatchSize
}

type BalancedMemoryEstimate struct {
	RiderCount             int    `json:"riderCount"`
	TopK                   int    `json:"topK"`
	ReorderWindow          int    `json:"reorderWindow"`
	RiderSliceBytes        uint64 `json:"riderSliceBytes"`
	BatchJobPointerBytes   uint64 `json:"batchJobPointerBytes"`
	BatchEnvelopeBytes     uint64 `json:"batchEnvelopeBytes"`
	LatencyHistogramBytes  uint64 `json:"latencyHistogramBytes"`
	GlobalRiderLoadBytes   uint64 `json:"globalRiderLoadBytes"`
	CandidateStorageBytes  uint64 `json:"candidateStorageBytes"`
	CandidateEnvelopeBytes uint64 `json:"candidateEnvelopeBytes"`
	CoreLowerBoundBytes    uint64 `json:"coreLowerBoundBytes"`
}

func EstimateBalancedMemory(riderCount int, options BalancedOptions) (BalancedMemoryEstimate, error) {
	if err := options.Validate(riderCount); err != nil {
		return BalancedMemoryEstimate{}, err
	}
	window := options.ReorderWindow()
	bufferCount := options.ChannelCapacity + options.Workers
	riderBytes, ok := multiply(uint64(riderCount), uint64(unsafe.Sizeof(model.Rider{})))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced rider slice byte estimate overflow")
	}
	bufferOrders, ok := multiply(uint64(bufferCount), uint64(options.BatchSize))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced order buffer count estimate overflow")
	}
	jobPointerBytes, ok := multiply(bufferOrders, uint64(unsafe.Sizeof((*candidateResult)(nil))))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced batch job pointer byte estimate overflow")
	}
	batchEnvelopeBytes, ok := multiply(uint64(bufferCount), uint64(unsafe.Sizeof(balancedBatch{})))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced batch envelope byte estimate overflow")
	}
	latencyBytes, ok := multiply(3, uint64(unsafe.Sizeof(latencyHistogram{})))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced latency histogram byte estimate overflow")
	}
	loadBytes, ok := multiply(uint64(riderCount), uint64(unsafe.Sizeof(uint64(0))))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced rider load byte estimate overflow")
	}
	candidateCount, ok := multiply(uint64(window), uint64(options.TopK))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced candidate count estimate overflow")
	}
	candidateBytes, ok := multiply(candidateCount, uint64(unsafe.Sizeof(matchrule.Candidate{})))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced candidate byte estimate overflow")
	}
	envelopeBytes, ok := multiply(uint64(window), uint64(unsafe.Sizeof(candidateResult{})))
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced envelope byte estimate overflow")
	}
	total, ok := add(riderBytes, jobPointerBytes, batchEnvelopeBytes, latencyBytes, loadBytes, candidateBytes, envelopeBytes)
	if !ok {
		return BalancedMemoryEstimate{}, errors.New("balanced memory estimate overflow")
	}
	return BalancedMemoryEstimate{
		RiderCount:             riderCount,
		TopK:                   options.TopK,
		ReorderWindow:          window,
		RiderSliceBytes:        riderBytes,
		BatchJobPointerBytes:   jobPointerBytes,
		BatchEnvelopeBytes:     batchEnvelopeBytes,
		LatencyHistogramBytes:  latencyBytes,
		GlobalRiderLoadBytes:   loadBytes,
		CandidateStorageBytes:  candidateBytes,
		CandidateEnvelopeBytes: envelopeBytes,
		CoreLowerBoundBytes:    total,
	}, nil
}

// 一个订单经过 Candidate Worker 处理之后的中间结果
type candidateResult struct {
	order      model.Order
	admittedNs int64   // 订单进入 Pipeline 的时间
	candidates []matchrule.Candidate  // Coordinator 根据这里的 candidates 决定最终骑手
	candidateSearchNs int64  // 当前订单执行 TopKInto 的纯计算时间
}

type balancedSelection struct {
	assignment       model.Assignment
	riderIndex       int
	assigned         bool   // true：成功找到骑手 false：候选骑手都达到额度
	capacityFiltered uint64 // 本单因为满额而跳过了多少名候选骑手
}

// 一个 Batch 包含多个订单
// Worker 不需要每次从 channel 拿一个订单，而是：
// 拿一个 batch
// 连续处理 4 个订单
// 减少 channel 通信开销
type balancedBatch struct {
	jobs       []*candidateResult
	admittedNs atomic.Int64
}

type candidateWorkerResult struct {
	workerID int
	count    uint64
	err      error
}

type coordinatorResult struct {
	completed         uint64  // 已完成分配的订单数量
	lastCompletionNs  int64
	maxReorderDepth   int
	preview           []model.Assignment
	reporter          *report.Accumulator
	queueMatchLatency latencyHistogram
	endToEndLatency   latencyHistogram
	err               error
	completedWithinWindow uint64  // 指定时间窗口内完成分配的订单数量
	candidateSearchLatency latencyHistogram
	assignmentDecisionLatency latencyHistogram
	algorithmComputeLatency latencyHistogram
	deferred                 uint64
	deferredByCapacity       uint64
	deferredByWindow         uint64
	deferredPreview          []DeferredOrder
	capacityFiltered         uint64
}

type CandidatePanicError struct {
	WorkerID  int
	OrderID   uint64
	Sequence  uint64
	Recovered any
	Stack     string
}

func (e *CandidatePanicError) Error() string {
	return fmt.Sprintf("candidate worker %d panicked on order ID %d sequence %d: %v\n%s", e.WorkerID, e.OrderID, e.Sequence, e.Recovered, e.Stack)
}

// 实际执行
func RunBalanced(
	ctx context.Context,
	source OrderSource,
	matcher CandidateMatcher,
	riders []model.Rider,
	options BalancedOptions,
	deferredSink DeferredOrderSink,
) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("context cannot be nil")
	}
	if source == nil {
		return Result{}, errors.New("order source cannot be nil")
	}
	if matcher == nil {
		return Result{}, errors.New("candidate matcher cannot be nil")
	}
	if err := options.Validate(len(riders)); err != nil {
		return Result{}, err
	}

	// 任何一个 Producer / Worker / Coordinator 出错，
	// 都可以调用 cancel() 通知其他 Goroutine 停止
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	runStarted := time.Now()
	batchChannel := make(chan *balancedBatch, options.ChannelCapacity)
	batchPool := make(chan *balancedBatch, options.ChannelCapacity+options.Workers)
	for range cap(batchPool) {
		batchPool <- &balancedBatch{jobs: make([]*candidateResult, 0, options.BatchSize)}
	}
	reorderWindow := options.ReorderWindow()

	// 提前创建对象
	candidatePool := make(chan *candidateResult, reorderWindow)
	for range cap(candidatePool) {
		candidatePool <- &candidateResult{candidates: make([]matchrule.Candidate, 0, options.TopK)}
	}

	candidateChannel := make(chan *candidateResult, options.ChannelCapacity)
	var maxCandidateQueueDepth atomic.Int64
	producerResults := make(chan producerResult, 1)
	workerResults := make(chan candidateWorkerResult, options.Workers)
	coordinatorResults := make(chan coordinatorResult, 1)

	go produceBalanced(runContext, cancel, runStarted, source, batchChannel, batchPool, candidatePool, options.Options, producerResults)
	
	// 只有一个 Coordinator Goroutine
	go coordinateBalanced(runContext, cancel, runStarted, candidateChannel, candidatePool, riders, options, coordinatorResults, deferredSink)

	var workers sync.WaitGroup
	workers.Add(options.Workers)
	for workerID := 0; workerID < options.Workers; workerID++ {
		go func() {
			defer workers.Done()
			findCandidates(runContext, cancel, workerID, matcher, batchChannel, batchPool, candidateChannel, candidatePool, options.TopK, &maxCandidateQueueDepth, workerResults)
		}()
	}

	producerState := <-producerResults
	workers.Wait()
	close(candidateChannel)
	coordinatorState := <-coordinatorResults
	close(workerResults)

	workerStates := make([]candidateWorkerResult, 0, options.Workers)
	for state := range workerResults {
		workerStates = append(workerStates, state)
	}
	sort.Slice(workerStates, func(first, second int) bool { return workerStates[first].workerID < workerStates[second].workerID })
	workerCompleted := make([]uint64, options.Workers)
	var workerError error
	for _, state := range workerStates {
		workerCompleted[state.workerID] = state.count
		if state.err != nil && workerError == nil {
			workerError = state.err
		}
	}

	result := Result{
		GeneratedOrders:      producerState.generated,
		AdmittedOrders:       producerState.admitted,
		CompletedOrders:      coordinatorState.completed,
		UnfinishedOrders:     unfinished(options.ExpectedOrders, coordinatorState.completed),
		LastPlannedArrivalNs: producerState.lastPlannedArrivalNs,
		MaxQueueDepth:        producerState.maxQueueDepth,
		WorkerCompleted:      workerCompleted,
		OrderPreview:         append([]model.Order(nil), producerState.preview...),
		AssignmentPreview:    append([]model.Assignment(nil), coordinatorState.preview...),
		Report:               coordinatorState.reporter.Summary(),
		Performance:          buildPerformanceMetrics(producerState, coordinatorState.lastCompletionNs, coordinatorState.queueMatchLatency, coordinatorState.endToEndLatency),
		StrategyDetails: &StrategyMetrics{
			TopK:                     options.TopK,
			MaxExtraDistanceMeters:   options.MaxExtraDistanceMeters,
			ReorderWindow:            reorderWindow,
			MaxCandidateQueueDepth:   int(maxCandidateQueueDepth.Load()),
			MaxReorderDepth:          coordinatorState.maxReorderDepth,
			CandidateWorkerCompleted: append([]uint64(nil), workerCompleted...),
		},
		DeferredOrders: coordinatorState.deferred,
		DeferredByWindow: coordinatorState.deferredByWindow,
		DeferredByCapacity: coordinatorState.deferredByCapacity,
		DeferredPreview: coordinatorState.deferredPreview,
		MaxOrdersPerRider: options.MaxOrdersPerRider,
		CapacityFilteredCandidates: coordinatorState.capacityFiltered,
	}

	result.GeneratedWithinWindow = producerState.generatedWithinWindow
	result.AdmittedWithinWindow = producerState.admittedWithinWindow
	result.CompletedWithinWindow = coordinatorState.completedWithinWindow

	//
	candidateSearch := coordinatorState.candidateSearchLatency.summary()
	assignmentDecision := coordinatorState.assignmentDecisionLatency.summary()
	algorithmCompute := coordinatorState.algorithmComputeLatency.summary()

	result.Performance.AlgorithmPerformanceMetrics.CandidateSearch = candidateSearch
	result.Performance.AlgorithmPerformanceMetrics.AssignmentDecision = assignmentDecision
	result.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute = algorithmCompute


	cause := firstNonCancellation(coordinatorState.err, workerError, producerState.err)
	if cause == nil {
		cause = firstError(coordinatorState.err, workerError, producerState.err)
	}
	// if cause == nil && (result.GeneratedOrders != options.ExpectedOrders || result.AdmittedOrders != options.ExpectedOrders ||
	// 	 result.Report.AssignmentCount != options.ExpectedOrders ||
	// 	result.Report.RiderOrderCountSum != options.ExpectedOrders) {
	// 	cause = errors.New("balanced completion violated order-count conservation")
	// }
	if cause != nil {
		return result, &RunError{Cause: cause, Generated: result.GeneratedOrders, Admitted: result.AdmittedOrders, Completed: result.CompletedOrders, Expected: options.ExpectedOrders}
	}
	return result, nil
}

func firstNonCancellation(values ...error) error {
	for _, value := range values {
		if value != nil && !errors.Is(value, context.Canceled) && !errors.Is(value, context.DeadlineExceeded) {
			return value
		}
	}
	return nil
}

func firstError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// source.Next()
//  等待 PlannedArrival
//  从 candidatePool 获取 envelope
//  凑成 Batch 后发送
func produceBalanced(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	source OrderSource,
	batches chan<- *balancedBatch,
	pool chan *balancedBatch,
	resultPool chan *candidateResult,
	options Options,
	results chan<- producerResult,
) {
	state := producerResult{preview: make([]model.Order, 0, options.PreviewSize)}
	var owned *balancedBatch
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer func() {
		timer.Stop()
		if recovered := recover(); recovered != nil {
			state.err = &producerPanicError{Recovered: recovered, Stack: string(debug.Stack())}
			cancel()
		}
		if owned != nil {
			for _, job := range owned.jobs {
				job.candidates = job.candidates[:0]
				resultPool <- job
			}
			recycleBalancedBatch(pool, owned)
		}
		close(batches)
		results <- state
	}()

	for {
		select {
		case <-ctx.Done():
			state.err = ctx.Err()
			return
		case owned = <-pool:
			owned.jobs = owned.jobs[:0]
			owned.admittedNs.Store(0)
		}
		sourceEnded := false
		for len(owned.jobs) < options.BatchSize {
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
			state.generated++   // 已生成的订单数量
			generatedAt := time.Since(start)
			if generatedAt < options.ObservationWindow {
				state.generatedWithinWindow += 1
			}

			state.lastPlannedArrivalNs = order.PlannedArrivalNs
			if len(state.preview) < options.PreviewSize {
				state.preview = append(state.preview, order)
			}
			if err := waitForPlannedArrival(ctx, start, order.PlannedArrivalNs, timer); err != nil {
				state.err = err
				return
			}
			var envelope *candidateResult
			select {
			// 从 Pool 取对象
			case envelope = <-resultPool:
			case <-ctx.Done():
				state.err = ctx.Err()
				return
			}
			envelope.order = order
			envelope.candidates = envelope.candidates[:0]
			owned.jobs = append(owned.jobs, envelope)
		}
		if len(owned.jobs) != 0 {
			select {
			case batches <- owned:
				admittedNs := time.Since(start).Nanoseconds()
				for _, job := range owned.jobs {
					job.admittedNs = admittedNs
					state.admissionLatency.observe(admittedNs - job.order.PlannedArrivalNs)
				}
				owned.admittedNs.Store(admittedNs + 1)

				// 读取 Batch 中订单数
				state.admitted += uint64(len(owned.jobs))

				if admittedNs < options.ObservationWindow.Nanoseconds() {
					state.admittedWithinWindow += uint64(len(owned.jobs))
				}

				state.lastAdmissionNs = admittedNs

				// 转移所有权并把 owned 设为 nil
				owned = nil
				if depth := len(batches); depth > state.maxQueueDepth {
					state.maxQueueDepth = depth
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

func findCandidates(
	ctx context.Context,
	cancel context.CancelFunc,
	workerID int,
	matcher CandidateMatcher,
	batches <-chan *balancedBatch,
	batchPool chan *balancedBatch,
	results chan<- *candidateResult,
	resultPool chan *candidateResult,
	topK int,
	maxQueueDepth *atomic.Int64,
	workerResults chan<- candidateWorkerResult,
) {
	state := candidateWorkerResult{workerID: workerID}
	var ownedBatch *balancedBatch
	nextJob := 0
	var current model.Order
	defer func() {
		if recovered := recover(); recovered != nil {
			state.err = &CandidatePanicError{WorkerID: workerID, OrderID: current.ID, Sequence: current.Sequence, Recovered: recovered, Stack: string(debug.Stack())}
			cancel()
		}
		if ownedBatch != nil {
			for ; nextJob < len(ownedBatch.jobs); nextJob++ {
				envelope := ownedBatch.jobs[nextJob]
				envelope.candidates = envelope.candidates[:0]
				resultPool <- envelope
			}
			recycleBalancedBatch(batchPool, ownedBatch)
		}
		workerResults <- state
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-batches:
			if !ok {
				return
			}
			ownedBatch = batch
			nextJob = 0
		}
		admittedNs := int64(0)
		for admittedNs = ownedBatch.admittedNs.Load(); admittedNs == 0; admittedNs = ownedBatch.admittedNs.Load() {
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		admittedNs--
		for nextJob < len(ownedBatch.jobs) {
			envelope := ownedBatch.jobs[nextJob]
			current = envelope.order
			envelope.admittedNs = admittedNs

			searchStarted := time.Now()

			// 计算 Top-K
			candidates, err := matcher.TopKInto(envelope.order, topK, envelope.candidates[:0])
			if err != nil {
				envelope.candidates = envelope.candidates[:0]
				resultPool <- envelope
				nextJob++
				state.err = fmt.Errorf("candidate worker %d query order ID %d sequence %d: %w", workerID, envelope.order.ID, envelope.order.Sequence, err)
				cancel()
				return
			}

			searchDuration := time.Since(searchStarted)

			envelope.candidates = candidates

			envelope.candidateSearchNs = searchDuration.Nanoseconds()

			select {
			case results <- envelope:
				state.count++
				nextJob++
				observeAtomicMax(maxQueueDepth, int64(len(results)))
			case <-ctx.Done():
				envelope.candidates = envelope.candidates[:0]
				resultPool <- envelope  
				nextJob++
				return
			}
		}
		recycleBalancedBatch(batchPool, ownedBatch)
		ownedBatch = nil
	}
}

func recycleBalancedBatch(pool chan *balancedBatch, batch *balancedBatch) {
	select {
	case pool <- batch:
	default:
	}
}

func observeAtomicMax(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current; current = target.Load() {
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

func coordinateBalanced(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	results <-chan *candidateResult,
	resultPool chan *candidateResult,
	riders []model.Rider,
	options BalancedOptions,
	output chan<- coordinatorResult,
	deferredSink DeferredOrderSink,
) {
	reporter, err := report.New(riders)
	state := coordinatorResult{preview: make([]model.Assignment, 0, options.PreviewSize), deferredPreview: make([]DeferredOrder, 0, options.PreviewSize), reporter: reporter}
	if err != nil {
		state.err = fmt.Errorf("create balanced reporter: %w", err)
		cancel()
		output <- state
		return
	}
	uidIndex := make(map[uint64]int, len(riders))

	// 只有 Coordinator 可以修改 loads
	// 所以：不需要 mutex、不需要 atomic.Uint64[]
	loads := make([]uint64, len(riders))
	for index, rider := range riders {
		uidIndex[rider.UID] = index
	}

	// 乱序缓冲区
	pending := make(map[uint64]*candidateResult, options.ReorderWindow())
	next := uint64(0)
	defer func() {
		if recovered := recover(); recovered != nil {
			state.err = fmt.Errorf("assignment coordinator panicked: %v\n%s", recovered, debug.Stack())
			cancel()
		}
		output <- state
	}()

	for {
		select {
		case <-ctx.Done():
			if state.err == nil {
				state.err = ctx.Err()
			}
			return
		case candidate, ok := <-results:
			if !ok {
				if len(pending) != 0 || next != options.ExpectedOrders {
					state.err = fmt.Errorf("candidate stream ended with next sequence %d, expected %d and %d pending", next, options.ExpectedOrders, len(pending))
				}
				return
			}
			if candidate.order.Sequence >= options.ExpectedOrders {
				state.err = fmt.Errorf("candidate sequence %d exceeds expected order count %d", candidate.order.Sequence, options.ExpectedOrders)
				cancel()
				return
			}
			if _, duplicate := pending[candidate.order.Sequence]; duplicate || candidate.order.Sequence < next {
				state.err = fmt.Errorf("duplicate candidate sequence %d", candidate.order.Sequence)
				cancel()
				return
			}
			pending[candidate.order.Sequence] = candidate
			if len(pending) > state.maxReorderDepth {
				state.maxReorderDepth = len(pending)
			}

			// Worker 可以乱序计算，但 Coordinator 必须顺序提交
			for {
				ready, exists := pending[next]
				if !exists {
					break
				}

				// 搜索耗时
				state.candidateSearchLatency.observe(ready.candidateSearchNs)

				decisionStarted := time.Now() //

				if options.AssignmentWindow > 0 && time.Since(start) >= options.AssignmentWindow {
					// 超时，不能分配
					deferredAt := time.Since(start).Nanoseconds()

					deferredOrder := DeferredOrder{
						Order: ready.order,
						Reason: DeferredReasonWindowExpired,
						DeferredAtNs: deferredAt,
						Attempt: options.Attempt,
					}


					if err := deferredSink.Store(ctx, deferredOrder); err != nil {
						state.err = fmt.Errorf("sink'store is wrong: %s", err)
						cancel()
						return
					}


					state.deferred += 1 //
					state.deferredByWindow += 1
				
					delete(pending, next)
					next++
					// ready.candidates清空
					ready.candidates = ready.candidates[:0] // [:0]不是释放底层数组，而是清空内容
					// ready放回resultPool
					resultPool <- ready
				
					continue
				}

				// chooseBalanced()运行订单的骑手分配算法-完成最终骑手选择
				balancedSelection, err := chooseBalanced(ready.order, ready.candidates, loads, uidIndex, options.MaxExtraDistanceMeters, options.MaxOrdersPerRider)
				if err != nil {
					state.err = err
					cancel()
					return
				}

				// 执行决策-骑手分配 耗时
				decisionDuration := time.Since(decisionStarted)
				state.assignmentDecisionLatency.observe(decisionDuration.Nanoseconds())

				// 每单算法总耗时
				algorithmComputeNs := decisionDuration.Nanoseconds() + ready.candidateSearchNs
				state.algorithmComputeLatency.observe(algorithmComputeNs)

				// 累计 selection.capacityFiltered
				state.capacityFiltered += balancedSelection.capacityFiltered

				if balancedSelection.assigned == false {
					// 全部满额度
					deferredAt := time.Since(start).Nanoseconds()

					deferredOrder := DeferredOrder{
						Order: ready.order,
						Reason: DeferredReasonCapacityExhausted,
						DeferredAtNs: deferredAt,
						Attempt: options.Attempt,
					}


					if err := deferredSink.Store(ctx, deferredOrder); err != nil {
						state.err = fmt.Errorf("sink'store is wrong: %s", err)
						cancel()
						return
					}


					state.deferred += 1 //
					state.deferredByCapacity += 1

					if len(state.deferredPreview) < options.PreviewSize {
						state.deferredPreview = append(
							state.deferredPreview, 
							DeferredOrder{
								Order: ready.order,
								Reason: DeferredReasonCapacityExhausted,
								DeferredAtNs: deferredAt,
							},
						)
					}

				} else {
					loads[balancedSelection.riderIndex] ++ // 骑手订单增加
					
					// Reporter 成功记录 Assignment
					if err := reporter.Observe(balancedSelection.assignment); err != nil {
						state.err = err
						cancel()
						return
					}
					
					completedAt := time.Now()
					
					state.lastCompletionNs = completedAt.Sub(start).Nanoseconds()
					state.queueMatchLatency.observe(state.lastCompletionNs - ready.admittedNs)
					state.endToEndLatency.observe(completedAt.Sub(start.Add(time.Duration(ready.order.PlannedArrivalNs))).Nanoseconds())
					
					state.completed++  // 已完成分配的订单数量

					elapsed := time.Since(start)
					if elapsed < options.ObservationWindow {
						state.completedWithinWindow += 1 //
					}

					if balancedSelection.assignment.Sequence < uint64(options.PreviewSize) {
						state.preview = append(state.preview, balancedSelection.assignment)
					}

				}

				
				delete(pending, next)
				next++
				// ready.candidates清空
				ready.candidates = ready.candidates[:0] // [:0]不是释放底层数组，而是清空内容
				// ready放回resultPool
				resultPool <- ready
			}
		}
	}
}

// 在候选骑手中选择最终骑手
func chooseBalanced(
	order model.Order,
	candidates []matchrule.Candidate,
	loads []uint64,
	uidIndex map[uint64]int,
	maxExtraDistanceMeters float64,
	maxOrdersPerRider int,
) (balancedSelection, error) {
	if len(candidates) == 0 {
		return balancedSelection{}, errors.New("balanced candidate list is empty")
	}

	// 使用当前第一个候选骑手计算最近距离
	// 即使第一个骑手已经满额，距离基准仍然应该是“真正最近骑手”
	nearestMeters := math.Sqrt(candidates[0].DistanceSquaredMeters)
	
	maxDistanceSquared := (nearestMeters + maxExtraDistanceMeters) * (nearestMeters + maxExtraDistanceMeters)
	
	
	// selected := candidates[0]
	// selectedIndex, exists := uidIndex[selected.RiderUID]
	// if !exists {
	// 	return model.Assignment{}, 0, fmt.Errorf("candidate rider UID %d is unknown", selected.RiderUID)
	// }
	selectedExists := false
	selected := matchrule.Candidate{}
	selectedIndex := -1
	capacityFiltered := 0

	// 遍历所有candidate
	for _, candidate := range candidates[0:] {
		if candidate.DistanceSquaredMeters > maxDistanceSquared {
			break
		}
		index, exists := uidIndex[candidate.RiderUID]
		if !exists {
			return balancedSelection{}, fmt.Errorf("candidate rider UID %d is unknown", candidate.RiderUID)
		}
		if index < 0 || index >= len(loads) {
			return balancedSelection{}, fmt.Errorf("rider's index out of range")
		}

		// 是否设置了骑手的订单限额 (0表示不限额)
		hasLimit := maxOrdersPerRider > 0

		isFull := hasLimit && loads[index] >= uint64(maxOrdersPerRider)

		if isFull {
			// 该骑手满额度
			capacityFiltered += 1
			continue
		}

		// 没有满额度且未选初始骑手
		if !selectedExists {
			selected = candidate
			selectedIndex = index
			selectedExists = true
			continue
		}

		// 在距离允许范围内选择“最空闲骑手”
		// 订单少；订单量相同时，谁距离更好；
		if loads[index] < loads[selectedIndex] ||
			(loads[index] == loads[selectedIndex] && matchrule.CandidateLess(candidate, selected)) {
			selected = candidate
			selectedIndex = index
		}
	}

	if selectedExists == false {
		return balancedSelection{
			assigned: false,
			capacityFiltered: uint64(capacityFiltered),
		}, nil
	} else {
		return balancedSelection{
			 assignment: model.Assignment{OrderID: order.ID, Sequence: order.Sequence, RiderUID: selected.RiderUID, DistanceSquaredMeters: selected.DistanceSquaredMeters},
			 riderIndex: selectedIndex,
			 assigned: true,
			 capacityFiltered: uint64(capacityFiltered),
			}, nil
	}

}
