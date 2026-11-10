package matching

import (
	"context"
	"errors"
	"fmt"
	"math"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/domain/rider"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"ride-sharing/internal/app/report"
)

// 找到订单附近的 Top-K 个候选骑手，再从“距离可以接受”的候选骑手里优先选择当前订单量较少的骑手
/*
不能让 Worker 直接完成分配：1.数据竞争 2.加锁也结果不确定
所以：并行计算候选人，串行完成最终决策
*/

// 找出距离最近的 K 个骑手
type CandidateMatcher interface {
	TopKInto(order.Order, int, []matching.Candidate) ([]matching.Candidate, error)
}

type BalancedOptions struct {
	Options
	TopK                   int           // 每个订单先找最近的 K 个骑手
	MaxExtraDistanceMeters float64       // 允许参与负载均衡的最远距离
	MaxOrdersPerRider      int           // 每个骑手的订单上限
	AssignmentWindow       time.Duration // 主分配阶段时长 == 订单总生成窗口arrival window
	Attempt                uint32
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
	riderBytes, ok := multiply(uint64(riderCount), uint64(unsafe.Sizeof(rider.Rider{})))
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
	latencyBytes, ok := multiply(3, uint64(unsafe.Sizeof(report.LatencyHistogram{})))
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
	candidateBytes, ok := multiply(candidateCount, uint64(unsafe.Sizeof(matching.Candidate{})))
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
	order             order.Order
	admittedNs        int64                // 订单进入 Pipeline 的时间
	candidates        []matching.Candidate // Coordinator 根据这里的 candidates 决定最终骑手
	candidateSearchNs int64                // 当前订单执行 TopKInto 的纯计算时间
}

type balancedSelection struct {
	assignment       matching.Assignment
	riderIndex       int
	assigned         bool   // true：成功找到骑手 false：候选骑手都达到额度
	capacityFiltered uint64 // 本单因为满额而跳过了多少名候选骑手
	blockedByReservation bool  
}

// 一个 Batch 包含多个订单
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
	completed                 uint64 // 已完成分配的订单数量
	lastCompletionNs          int64
	maxReorderDepth           int
	preview                   []matching.Assignment
	reporter                  *report.Accumulator
	queueMatchLatency         report.LatencyHistogram
	endToEndLatency           report.LatencyHistogram
	err                       error
	completedWithinWindow     uint64 // 指定时间窗口内完成分配的订单数量
	candidateSearchLatency    report.LatencyHistogram
	assignmentDecisionLatency report.LatencyHistogram
	algorithmComputeLatency   report.LatencyHistogram
	deferred                  uint64
	deferredByCapacity        uint64
	deferredByWindow          uint64
	deferredPreview           []DeferredOrder
	capacityFiltered          uint64
	attempts 				  []domaindispatch.Attempt
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

func RunBalanced(
	ctx context.Context,
	source OrderSource,
	matcher CandidateMatcher,
	riders []rider.Rider,
	options BalancedOptions,
	deferredSink DeferredOrderSink,
) (Result, error) {
	return runBalanced(
		ctx, source, matcher, riders, options, deferredSink, nil, nil,
	)
}

func RunBalancedWithOffers(
	ctx context.Context,
	source OrderSource,
	matcher CandidateMatcher,
	riders []rider.Rider,
	options BalancedOptions,
	deferredSink DeferredOrderSink,
	confirmer OfferConfirmer,
	ledger OfferLedger,
) (Result, error) {
	if confirmer == nil {
		return Result{}, errors.New("offer confirmer cannot be nil")
	}
	if deferredSink == nil {
		return Result{}, errors.New("deferred sink cannot be nil")
	}
	if ledger == nil {
		return Result{}, errors.New("offer ledger cannot be nil")
	}

	return runBalanced(
		ctx, source, matcher, riders, options, deferredSink, confirmer, ledger,
	)
}

// runBalanced:
// Producer：生成订单并组装 Batch
// -> batchChannel：批量订单队列 (Worker1, Worker2...)
// -> candidateChannel：候选结果队列
// -> Coordinator：按 Sequence 顺序分配 (选择骑手 → 更新 loads → 统计结果)
func runBalanced(
	ctx context.Context,
	source OrderSource,
	matcher CandidateMatcher,
	riders []rider.Rider,
	options BalancedOptions,
	deferredSink DeferredOrderSink,
	confirmer OfferConfirmer,
	ledger OfferLedger,
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
	// 调用 cancel() 通知其他 Goroutine 停止
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	runStarted := time.Now()
	// 创建 Batch 队列: Producer 将一批订单发送给 Worker
	batchChannel := make(chan *balancedBatch, options.ChannelCapacity)
	// 创建 Batch 对象池: 复用 Batch
	batchPool := make(chan *balancedBatch, options.ChannelCapacity+options.Workers)
	for range cap(batchPool) {
		batchPool <- &balancedBatch{jobs: make([]*candidateResult, 0, options.BatchSize)}
	}
	reorderWindow := options.ReorderWindow()

	// 创建候选结果对象池 
	candidatePool := make(chan *candidateResult, reorderWindow)
	for range cap(candidatePool) {
		candidatePool <- &candidateResult{candidates: make([]matching.Candidate, 0, options.TopK)}
	}

	// Worker 将计算好的候选结果发送给 Coordinator
	candidateChannel := make(chan *candidateResult, options.ChannelCapacity)
	
	// 创建结果通知 Channel
	var maxCandidateQueueDepth atomic.Int64
	producerResults := make(chan producerResult, 1)
	workerResults := make(chan candidateWorkerResult, options.Workers)
	coordinatorResults := make(chan coordinatorResult, 1)

	// 启动一个 Producer: 生成订单、控制到达时间、组装 Batch
	go produceBalanced(runContext, cancel, runStarted, source, 
		batchChannel, batchPool, candidatePool, options.Options, 
		producerResults)

	// 启动一个 Coordinator: 最终骑手选择和订单量统计
	var offers *offerPool

	if confirmer == nil {
		go coordinateBalanced(
			runContext, cancel, runStarted,
			candidateChannel, candidatePool,
			riders, options, coordinatorResults, deferredSink,
		)
	} else {
		offers = startOfferPool(
			runContext,
			confirmer,
			options.Workers,
			options.ChannelCapacity,
		)
		go coordinateBalancedOffers(
			runContext, cancel, runStarted,
			candidateChannel, candidatePool,
			riders, options,
			offers.jobs, offers.results,
			coordinatorResults, deferredSink,
			ledger,
		)
	}

	// 启动多个 Worker
	var workers sync.WaitGroup
	workers.Add(options.Workers)
	for workerID := 0; workerID < options.Workers; workerID++ {
		go func() {
			defer workers.Done()
			// 每个 Worker 独立计算订单的 Top-K 候选骑手
			findCandidates(runContext, cancel, workerID, matcher, batchChannel, batchPool, candidateChannel, candidatePool, options.TopK, &maxCandidateQueueDepth, workerResults)
		}()
	}

	producerState := <-producerResults
	// 等待所有 Worker 结束
	workers.Wait()
	close(candidateChannel)

	coordinatorState := <-coordinatorResults

	if offers != nil {
		if coordinatorState.err != nil {
			cancel()
		}
		offers.stop()
	}

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

	reportSummary := report.Summary{}
	if coordinatorState.reporter != nil {
		reportSummary = coordinatorState.reporter.Summary()
	}

	// 汇总统计数据和错误信息
	result := Result{
		GeneratedOrders:      producerState.generated,
		AdmittedOrders:       producerState.admitted,
		CompletedOrders:      coordinatorState.completed,
		UnfinishedOrders:     unfinished(options.ExpectedOrders, coordinatorState.completed),
		LastPlannedArrivalNs: producerState.lastPlannedArrivalNs,
		MaxQueueDepth:        producerState.maxQueueDepth,
		WorkerCompleted:      workerCompleted,
		OrderPreview:         append([]order.Order(nil), producerState.preview...),
		AssignmentPreview:    append([]matching.Assignment(nil), coordinatorState.preview...),
		Report: 			  reportSummary,
		Performance:          buildPerformanceMetrics(producerState, coordinatorState.lastCompletionNs, coordinatorState.queueMatchLatency, coordinatorState.endToEndLatency),
		StrategyDetails: &StrategyMetrics{
			TopK:                     options.TopK,
			MaxExtraDistanceMeters:   options.MaxExtraDistanceMeters,
			ReorderWindow:            reorderWindow,
			MaxCandidateQueueDepth:   int(maxCandidateQueueDepth.Load()),
			MaxReorderDepth:          coordinatorState.maxReorderDepth,
			CandidateWorkerCompleted: append([]uint64(nil), workerCompleted...),
		},
		DeferredOrders:             coordinatorState.deferred,
		DeferredByWindow:           coordinatorState.deferredByWindow,
		DeferredByCapacity:         coordinatorState.deferredByCapacity,
		DeferredPreview:            coordinatorState.deferredPreview,
		MaxOrdersPerRider:          options.MaxOrdersPerRider,
		CapacityFilteredCandidates: coordinatorState.capacityFiltered,
		Attempts: 				    append([]domaindispatch.Attempt(nil),coordinatorState.attempts...,),
	}

	result.GeneratedWithinWindow = producerState.generatedWithinWindow
	result.AdmittedWithinWindow = producerState.admittedWithinWindow
	result.CompletedWithinWindow = coordinatorState.completedWithinWindow

	//
	candidateSearch := coordinatorState.candidateSearchLatency.Summary()
	assignmentDecision := coordinatorState.assignmentDecisionLatency.Summary()
	algorithmCompute := coordinatorState.algorithmComputeLatency.Summary()

	result.Performance.AlgorithmPerformanceMetrics.CandidateSearch = candidateSearch
	result.Performance.AlgorithmPerformanceMetrics.AssignmentDecision = assignmentDecision
	result.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute = algorithmCompute

	cause := firstNonCancellation(coordinatorState.err, workerError, producerState.err)
	if cause == nil {
		cause = firstError(coordinatorState.err, workerError, producerState.err)
	}
	
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

// produceBalanced 生成订单并组装 Batch
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
	state := producerResult{preview: make([]order.Order, 0, options.PreviewSize)}
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
			// 从对象池取出一个可复用的 Batch
			owned.jobs = owned.jobs[:0] // 清空旧订单，但保留底层数组
			owned.admittedNs.Store(0)
		}
		sourceEnded := false
		// 向 Batch 中填充订单
		for len(owned.jobs) < options.BatchSize {
			order, ok, err := source.Next() // 从订单生成器获取下一笔订单
			if err != nil {
				state.err = fmt.Errorf("generate order: %w", err)
				cancel()
				return
			}
			if !ok {
				sourceEnded = true
				break
			}
			state.generated++ // 已生成的订单数量
			generatedAt := time.Since(start)
			if generatedAt < options.ObservationWindow {
				state.generatedWithinWindow += 1
			}

			state.lastPlannedArrivalNs = order.PlannedArrivalNs
			if len(state.preview) < options.PreviewSize {
				state.preview = append(state.preview, order)
			}

			// 等待订单的计划到达时间
			if err := waitForPlannedArrival(ctx, start, order.PlannedArrivalNs, timer); err != nil {
				state.err = err
				return
			}

			var envelope *candidateResult
			select {
			// 从对象池取出一个 candidateResult
			case envelope = <-resultPool:
			case <-ctx.Done():
				state.err = ctx.Err()
				return
			}

			// 将订单放进对象
			envelope.order = order
			envelope.candidates = envelope.candidates[:0]
			// 加入当前 Batch
			owned.jobs = append(owned.jobs, envelope)
		}

		// 将 Batch 发送给 Worker
		if len(owned.jobs) != 0 {
			select {
			case batches <- owned:
				// 记录 Batch 进入 Pipeline 的时间
				admittedNs := time.Since(start).Nanoseconds()
				for _, job := range owned.jobs {
					job.admittedNs = admittedNs
					state.admissionLatency.Observe(admittedNs - job.order.PlannedArrivalNs)
				}
				owned.admittedNs.Store(admittedNs + 1)

				// 读取 Batch 中订单数
				state.admitted += uint64(len(owned.jobs))

				if admittedNs < options.ObservationWindow.Nanoseconds() {
					state.admittedWithinWindow += uint64(len(owned.jobs))
				}

				state.lastAdmissionNs = admittedNs

				// Producer 不再拥有这个 Batch, 后续由接收它的 Worker 负责处理和回收
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

// findCandidates 可由多个 Goroutine 同时执行，只负责查找Top-K 候选骑手
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
	var current order.Order
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
			// 从 Batch Channel 获取一批订单
			if !ok {
				return
			}
			ownedBatch = batch
			nextJob = 0
		}
		admittedNs := int64(0)
		// 等待 Producer 完成入队时间初始化
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

			// 查找距离当前订单最近的 K 个骑手
			candidates, err := matcher.TopKInto(envelope.order, topK, 
				envelope.candidates[:0])
			if err != nil {
				envelope.candidates = envelope.candidates[:0]
				resultPool <- envelope
				nextJob++
				state.err = fmt.Errorf("candidate worker %d query order ID %d sequence %d: %w", workerID, envelope.order.ID, envelope.order.Sequence, err)
				cancel()
				return
			}

			searchDuration := time.Since(searchStarted)

			// 保存候选骑手和搜索耗时
			envelope.candidates = candidates
			envelope.candidateSearchNs = searchDuration.Nanoseconds()

			select {
			case results <- envelope:
				// 发送给 Coordinator，等待决定最终分配给谁
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

		// 整批订单处理完毕, 将 Batch 放回对象池供 Producer 复用
		recycleBalancedBatch(batchPool, ownedBatch)
		// 所有权转移给 Coordinator
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

// coordinateBalanced 按订单顺序进行最终分配
func coordinateBalanced(
	ctx context.Context,
	cancel context.CancelFunc,
	start time.Time,
	results <-chan *candidateResult,
	resultPool chan *candidateResult,
	riders []rider.Rider,
	options BalancedOptions,
	output chan<- coordinatorResult,
	deferredSink DeferredOrderSink,
) {
	reporter, err := report.New(riders)
	state := coordinatorResult{preview: make([]matching.Assignment, 0, options.PreviewSize), deferredPreview: make([]DeferredOrder, 0, options.PreviewSize), reporter: reporter}
	if err != nil {
		state.err = fmt.Errorf("create balanced reporter: %w", err)
		cancel()
		output <- state
		return
	}
	uidIndex := make(map[uint64]int, len(riders))

	// 只有 Coordinator 可以修改 loads
	loads := make([]uint64, len(riders)) // loads[i] 第 i 个骑手已分配的订单数量
	for index, rider := range riders {
		uidIndex[rider.UID] = index
	}

	// 乱序缓冲区 (Worker 并行计算，完成顺序不一定等于订单生成顺序)
	pending := make(map[uint64]*candidateResult, options.ReorderWindow())
	// 下一个应该处理的订单序号
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

			// 将 Worker 计算结果放入乱序缓冲区
			pending[candidate.order.Sequence] = candidate
			if len(pending) > state.maxReorderDepth {
				state.maxReorderDepth = len(pending)
			}

			// Coordinator 必须顺序提交, 按 Sequence 顺序处理
			for {
				ready, exists := pending[next]
				if !exists {
					// 暂时停止，等待后续 Worker 结果
					break
				}

				// 搜索耗时
				state.candidateSearchLatency.Observe(ready.candidateSearchNs)

				decisionStarted := time.Now() //

				if options.AssignmentWindow > 0 && time.Since(start) >= options.AssignmentWindow {
					// 超出窗口：记录为延迟订单 deferredOrder
					deferredAt := time.Since(start).Nanoseconds()
					deferredOrder := DeferredOrder{
						Order:        ready.order,
						Reason:       DeferredReasonWindowExpired,
						DeferredAtNs: deferredAt,
						Attempt:      options.Attempt,
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

				// 根据距离、订单量和容量选择最终骑手
				balancedSelection, err := chooseBalanced(ready.order, ready.candidates, loads, uidIndex, options.MaxExtraDistanceMeters, options.MaxOrdersPerRider)
				if err != nil {
					state.err = err
					cancel()
					return
				}

				// 执行决策-骑手分配 耗时
				decisionDuration := time.Since(decisionStarted)
				state.assignmentDecisionLatency.Observe(decisionDuration.Nanoseconds())

				// 每单算法总耗时
				algorithmComputeNs := decisionDuration.Nanoseconds() + ready.candidateSearchNs
				state.algorithmComputeLatency.Observe(algorithmComputeNs)

				// 累计 selection.capacityFiltered
				state.capacityFiltered += balancedSelection.capacityFiltered

				if balancedSelection.assigned == false {
					// 候选骑手均无法接单, 记录为容量不足的延迟订单
					deferredAt := time.Since(start).Nanoseconds()
					deferredOrder := DeferredOrder{
						Order:        ready.order,
						Reason:       DeferredReasonCapacityExhausted,
						DeferredAtNs: deferredAt,
						Attempt:      options.Attempt,
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
								Order:        ready.order,
								Reason:       DeferredReasonCapacityExhausted,
								DeferredAtNs: deferredAt,
							},
						)
					}

				} else {
					// 正式提交分配结果 (更新 loads)
					if err := commitBalancedAssignment(
						start,
						ready,
						balancedSelection,
						loads,
						&state,
						options,
					); err != nil {
						state.err = err
						cancel()
						return
					}

				}

				// 删除已处理订单
				delete(pending, next)
				// 准备处理下一个订单
				next++
				
				// 清空候选结果并放回对象池
				ready.candidates = ready.candidates[:0] 
				resultPool <- ready
			}
		}
	}
}

// commitBalancedAssignment 正式提交一次骑手订单分配
// 在骑手明确接单之后，调用该函数 (completed 表示匹配分配完成)
// ready-当前订单及其候选骑手计算结果
// selection-chooseBalanced() 选出的骑手
// loads-每位骑手当前已分配的订单数
// state-Coordinator 维护的统计结果
func commitBalancedAssignment(
	start time.Time,
	ready *candidateResult,
	selection balancedSelection,
	loads []uint64,
	state *coordinatorResult,
	options BalancedOptions,
) error {

	// 确认订单是否已经找到可以分配的骑手
	if !selection.assigned {
		return errors.New("cannot commit an unassigned selection")
	}

	// 将本次分配结果交给 reporter 统计
	if err := state.reporter.Observe(selection.assignment); err != nil {
		return err
	}
	// 更新骑手订单量loads
	loads[selection.riderIndex]++

	// 记录当前订单完成匹配的时间
	completedAt := time.Now()
	state.lastCompletionNs = completedAt.Sub(start).Nanoseconds()
	// 统计订单从进入 Pipeline 到完成匹配的延迟
	state.queueMatchLatency.Observe(state.lastCompletionNs - ready.admittedNs)
	// 从订单计划到达到最终匹配完成的总耗时
	state.endToEndLatency.Observe(
		completedAt.Sub(
			start.Add(time.Duration(ready.order.PlannedArrivalNs)),
		).Nanoseconds(),
	)

	state.completed++

	if completedAt.Sub(start) < options.ObservationWindow {
		state.completedWithinWindow++
	}

	if selection.assignment.Sequence < uint64(options.PreviewSize) {
		// 保存少量分配结果作为预览
		state.preview = append(state.preview, selection.assignment)
	}

	return nil
}

// chooseBalanced 负载均衡算法, 在候选骑手中选择最终骑手
func chooseBalanced(
	order order.Order,
	candidates []matching.Candidate,
	loads []uint64,
	uidIndex map[uint64]int,
	maxExtraDistanceMeters float64,
	maxOrdersPerRider int,
) (balancedSelection, error) {
	if len(candidates) == 0 {
		return balancedSelection{}, errors.New("balanced candidate list is empty")
	}

	// 以最近骑手的距离作为基准 (candidates 已经按距离从近到远排列)
	nearestSquared := candidates[0].DistanceSquaredMeters
	maxDistanceSquared := nearestSquared
	// 计算允许的最远距离
	if maxExtraDistanceMeters > 0 {
		nearestMeters := math.Sqrt(candidates[0].DistanceSquaredMeters)
		extra := maxExtraDistanceMeters

		maxDistanceSquared = nearestSquared + 2*nearestMeters*extra + extra*extra
	}

	selectedExists := false
	selected := matching.Candidate{}
	selectedIndex := -1
	capacityFiltered := 0

	// 遍历 Top-K 候选骑手
	for _, candidate := range candidates[0:] {
		if candidate.DistanceSquaredMeters > maxDistanceSquared {
			// 超出允许距离范围
			break
		}

		// 找到骑手在 loads 中的下标
		index, exists := uidIndex[candidate.RiderUID]
		if !exists {
			return balancedSelection{}, fmt.Errorf("candidate rider UID %d is unknown", candidate.RiderUID)
		}
		if index < 0 || index >= len(loads) {
			return balancedSelection{}, fmt.Errorf("rider's index out of range")
		}

		// 检查骑手是否达到订单上限 (maxOrdersPerRider = 0 表示不限制)
		hasLimit := maxOrdersPerRider > 0
		isFull := hasLimit && loads[index] >= uint64(maxOrdersPerRider)
		if isFull {
			capacityFiltered += 1
			continue
		}
		// 第一个符合条件的骑手作为初始选择
		if !selectedExists {
			selected = candidate
			selectedIndex = index
			selectedExists = true
			continue
		}

		// 优先选择订单量更少的骑手
		// 订单量相同，则比较距离和 UID
		if loads[index] < loads[selectedIndex] ||
			(loads[index] == loads[selectedIndex] && matching.CandidateLess(candidate, selected)) {
			selected = candidate
			selectedIndex = index
		}
	}

	if selectedExists == false {
		// 没有找到可分配骑手
		return balancedSelection{
			assigned:         false,
			capacityFiltered: uint64(capacityFiltered),
		}, nil
	} else {
		// 返回最终选择
		return balancedSelection{
			assignment:       matching.Assignment{OrderID: order.ID, Sequence: order.Sequence, RiderUID: selected.RiderUID, DistanceSquaredMeters: selected.DistanceSquaredMeters},
			riderIndex:       selectedIndex,
			assigned:         true,
			capacityFiltered: uint64(capacityFiltered),
		}, nil
	}

}
