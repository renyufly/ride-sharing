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

type CandidateMatcher interface {
	TopKInto(model.Order, int, []matchrule.Candidate) ([]matchrule.Candidate, error)
}

type BalancedOptions struct {
	Options
	TopK                   int
	MaxExtraDistanceMeters float64
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
	return errors.Join(errs...)
}

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

type candidateResult struct {
	order      model.Order
	admittedNs int64
	candidates []matchrule.Candidate
}

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
	completed         uint64
	lastCompletionNs  int64
	maxReorderDepth   int
	preview           []model.Assignment
	reporter          *report.Accumulator
	queueMatchLatency latencyHistogram
	endToEndLatency   latencyHistogram
	err               error
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
	riders []model.Rider,
	options BalancedOptions,
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

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	runStarted := time.Now()
	batchChannel := make(chan *balancedBatch, options.ChannelCapacity)
	batchPool := make(chan *balancedBatch, options.ChannelCapacity+options.Workers)
	for range cap(batchPool) {
		batchPool <- &balancedBatch{jobs: make([]*candidateResult, 0, options.BatchSize)}
	}
	reorderWindow := options.ReorderWindow()
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
	go coordinateBalanced(runContext, cancel, runStarted, candidateChannel, candidatePool, riders, options, coordinatorResults)

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
	}

	cause := firstNonCancellation(coordinatorState.err, workerError, producerState.err)
	if cause == nil {
		cause = firstError(coordinatorState.err, workerError, producerState.err)
	}
	if cause == nil && (result.GeneratedOrders != options.ExpectedOrders || result.AdmittedOrders != options.ExpectedOrders ||
		result.CompletedOrders != options.ExpectedOrders || result.Report.AssignmentCount != options.ExpectedOrders ||
		result.Report.RiderOrderCountSum != options.ExpectedOrders) {
		cause = errors.New("balanced completion violated order-count conservation")
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
			state.generated++
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
				state.admitted += uint64(len(owned.jobs))
				state.lastAdmissionNs = admittedNs
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
			candidates, err := matcher.TopKInto(envelope.order, topK, envelope.candidates[:0])
			if err != nil {
				envelope.candidates = envelope.candidates[:0]
				resultPool <- envelope
				nextJob++
				state.err = fmt.Errorf("candidate worker %d query order ID %d sequence %d: %w", workerID, envelope.order.ID, envelope.order.Sequence, err)
				cancel()
				return
			}
			envelope.candidates = candidates
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
) {
	reporter, err := report.New(riders)
	state := coordinatorResult{preview: make([]model.Assignment, 0, options.PreviewSize), reporter: reporter}
	if err != nil {
		state.err = fmt.Errorf("create balanced reporter: %w", err)
		cancel()
		output <- state
		return
	}
	uidIndex := make(map[uint64]int, len(riders))
	loads := make([]uint64, len(riders))
	for index, rider := range riders {
		uidIndex[rider.UID] = index
	}
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
			for {
				ready, exists := pending[next]
				if !exists {
					break
				}
				assignment, riderIndex, err := chooseBalanced(ready.order, ready.candidates, loads, uidIndex, options.MaxExtraDistanceMeters)
				if err != nil {
					state.err = err
					cancel()
					return
				}
				loads[riderIndex]++
				if err := reporter.Observe(assignment); err != nil {
					state.err = err
					cancel()
					return
				}
				completedAt := time.Now()
				state.lastCompletionNs = completedAt.Sub(start).Nanoseconds()
				state.queueMatchLatency.observe(state.lastCompletionNs - ready.admittedNs)
				state.endToEndLatency.observe(completedAt.Sub(start.Add(time.Duration(ready.order.PlannedArrivalNs))).Nanoseconds())
				state.completed++
				if assignment.Sequence < uint64(options.PreviewSize) {
					state.preview = append(state.preview, assignment)
				}
				delete(pending, next)
				next++
				ready.candidates = ready.candidates[:0]
				resultPool <- ready
			}
		}
	}
}

func chooseBalanced(
	order model.Order,
	candidates []matchrule.Candidate,
	loads []uint64,
	uidIndex map[uint64]int,
	maxExtraDistanceMeters float64,
) (model.Assignment, int, error) {
	if len(candidates) == 0 {
		return model.Assignment{}, 0, errors.New("balanced candidate list is empty")
	}
	nearestMeters := math.Sqrt(candidates[0].DistanceSquaredMeters)
	maxDistanceSquared := (nearestMeters + maxExtraDistanceMeters) * (nearestMeters + maxExtraDistanceMeters)
	selected := candidates[0]
	selectedIndex, exists := uidIndex[selected.RiderUID]
	if !exists {
		return model.Assignment{}, 0, fmt.Errorf("candidate rider UID %d is unknown", selected.RiderUID)
	}
	for _, candidate := range candidates[1:] {
		if candidate.DistanceSquaredMeters > maxDistanceSquared {
			break
		}
		index, exists := uidIndex[candidate.RiderUID]
		if !exists {
			return model.Assignment{}, 0, fmt.Errorf("candidate rider UID %d is unknown", candidate.RiderUID)
		}
		if loads[index] < loads[selectedIndex] ||
			(loads[index] == loads[selectedIndex] && matchrule.CandidateLess(candidate, selected)) {
			selected = candidate
			selectedIndex = index
		}
	}
	return model.Assignment{OrderID: order.ID, Sequence: order.Sequence, RiderUID: selected.RiderUID, DistanceSquaredMeters: selected.DistanceSquaredMeters}, selectedIndex, nil
}
