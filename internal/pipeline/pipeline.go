// Package pipeline implements the bounded CSP worker pipeline for strategy A.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"ride-sharing/internal/model"
	"ride-sharing/internal/report"
)

type OrderSource interface {
	Next() (model.Order, bool, error)
}

type Matcher interface {
	Match(model.Order) (model.Assignment, error)
}

type Options struct {
	Workers         int
	BatchSize       int
	ChannelCapacity int
	ExpectedOrders  uint64
	PreviewSize     int
}

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
	WorkerCompleted      []uint64           `json:"workerCompletedOrders"`
	OrderPreview         []model.Order      `json:"-"`
	AssignmentPreview    []model.Assignment `json:"-"`
	Report               report.Summary     `json:"report"`
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
	preview              []model.Order
	err                  error
}

type workerResult struct {
	workerID  int
	completed uint64
	preview   []model.Assignment
	reporter  *report.Accumulator
	err       error
}

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

	mergedReporter, err := report.New(riders)
	if err != nil {
		return Result{}, fmt.Errorf("create pipeline reporter: %w", err)
	}
	workerReporters := make([]*report.Accumulator, options.Workers)
	for index := range workerReporters {
		workerReporters[index] = mergedReporter.Fork()
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	batchChannel := make(chan []model.Order, options.ChannelCapacity)
	bufferPool := make(chan []model.Order, options.ChannelCapacity+options.Workers)
	for range cap(bufferPool) {
		bufferPool <- make([]model.Order, 0, options.BatchSize)
	}
	producerResults := make(chan producerResult, 1)
	workerResults := make(chan workerResult, options.Workers)

	go produce(runContext, cancel, source, batchChannel, bufferPool, options, producerResults)

	var workers sync.WaitGroup
	workers.Add(options.Workers)
	for workerID := 0; workerID < options.Workers; workerID++ {
		go func() {
			defer workers.Done()
			work(runContext, cancel, workerID, matcher, batchChannel, bufferPool, workerReporters[workerID], options.PreviewSize, workerResults)
		}()
	}

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
	var workerError error
	for _, state := range states {
		result.CompletedOrders += state.completed
		result.WorkerCompleted[state.workerID] = state.completed
		result.AssignmentPreview = append(result.AssignmentPreview, state.preview...)
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

	cause := workerError
	if cause == nil {
		cause = producerState.err
	}
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

func produce(
	ctx context.Context,
	cancel context.CancelFunc,
	source OrderSource,
	batches chan<- []model.Order,
	pool chan []model.Order,
	options Options,
	results chan<- producerResult,
) {
	state := producerResult{preview: make([]model.Order, 0, options.PreviewSize)}
	var ownedBatch []model.Order
	defer func() {
		if recovered := recover(); recovered != nil {
			state.err = &producerPanicError{Recovered: recovered, Stack: string(debug.Stack())}
			cancel()
		}
		if ownedBatch != nil {
			recycleBuffer(pool, ownedBatch)
		}
		close(batches)
		results <- state
	}()

	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			state.err = ctx.Err()
			return
		case ownedBatch = <-pool:
			ownedBatch = ownedBatch[:0]
		}

		sourceEnded := false
		for len(ownedBatch) < options.BatchSize {
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
			if uint64(len(state.preview)) < uint64(options.PreviewSize) {
				state.preview = append(state.preview, order)
			}
			if err := waitForPlannedArrival(ctx, start, order.PlannedArrivalNs); err != nil {
				state.err = err
				return
			}
			ownedBatch = append(ownedBatch, order)
		}

		if len(ownedBatch) != 0 {
			batchSize := len(ownedBatch)
			select {
			case batches <- ownedBatch:
				ownedBatch = nil
				state.admitted += uint64(batchSize)
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

func work(
	ctx context.Context,
	cancel context.CancelFunc,
	workerID int,
	matcher Matcher,
	batches <-chan []model.Order,
	pool chan []model.Order,
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
	var ownedBatch []model.Order
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
			recycleBuffer(pool, ownedBatch)
		}
		results <- state
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
		}

		for index := range ownedBatch {
			select {
			case <-ctx.Done():
				return
			default:
			}
			currentOrder = ownedBatch[index]
			hasCurrentOrder = true
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
			if assignment.Sequence < uint64(previewSize) {
				state.preview = append(state.preview, assignment)
			}
			hasCurrentOrder = false
		}
		recycleBuffer(pool, ownedBatch)
		ownedBatch = nil
	}
}

func waitForPlannedArrival(ctx context.Context, start time.Time, plannedArrivalNs int64) error {
	if plannedArrivalNs <= 0 {
		return nil
	}
	wait := time.Until(start.Add(time.Duration(plannedArrivalNs)))
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func recycleBuffer(pool chan []model.Order, batch []model.Order) {
	select {
	case pool <- batch[:0]:
	default:
	}
}

func unfinished(expected, completed uint64) uint64 {
	if completed >= expected {
		return 0
	}
	return expected - completed
}
