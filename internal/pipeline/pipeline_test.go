package pipeline

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/matcher/kdtree"
	"ride-sharing/internal/model"
	"ride-sharing/internal/report"
)

func TestConcurrentPipelineMatchesSerialKDTree(t *testing.T) {
	const orderCount = 10_000
	dataGenerator, riders, matcher := testData(t, 100)
	wrapper := &trackingMatcher{matcher: matcher, seen: make([]uint8, orderCount)}
	stream, err := dataGenerator.NewOrderStream(orderCount, 0, generator.ArrivalUnbounded, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}

	result, err := Run(context.Background(), stream, wrapper, riders, Options{
		Workers:         4,
		BatchSize:       31,
		ChannelCapacity: 3,
		ExpectedOrders:  orderCount,
		PreviewSize:     3,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wrapper.assertExactlyOnce(t)
	assertConservation(t, result, orderCount)
	if result.MaxQueueDepth > 3 {
		t.Fatalf("MaxQueueDepth = %d, exceeds capacity 3", result.MaxQueueDepth)
	}
	if got := sum(result.WorkerCompleted); got != orderCount {
		t.Fatalf("sum(WorkerCompleted) = %d, want %d", got, orderCount)
	}
	for index := range 3 {
		if result.OrderPreview[index].Sequence != uint64(index) || result.AssignmentPreview[index].Sequence != uint64(index) {
			t.Fatalf("preview %d is not ordered: orders=%+v assignments=%+v", index, result.OrderPreview, result.AssignmentPreview)
		}
	}

	serial := serialSummary(t, dataGenerator, matcher, riders, orderCount)
	assertSummariesEquivalent(t, result.Report, serial)
}

func TestBoundedQueueAppliesBackpressureWithoutDropping(t *testing.T) {
	const orderCount = 40
	dataGenerator, riders, matcher := testData(t, 10)
	stream, err := dataGenerator.NewOrderStream(orderCount, 0, generator.ArrivalUnbounded, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}

	result, err := Run(context.Background(), stream, &trackingMatcher{matcher: matcher, delay: time.Millisecond}, riders, Options{
		Workers:         1,
		BatchSize:       1,
		ChannelCapacity: 1,
		ExpectedOrders:  orderCount,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertConservation(t, result, orderCount)
	if result.MaxQueueDepth != 1 {
		t.Fatalf("MaxQueueDepth = %d, want bounded queue to reach 1", result.MaxQueueDepth)
	}
}

func TestCancellationReturnsPartialCountsAndStops(t *testing.T) {
	const orderCount = 1_000
	dataGenerator, riders, matcher := testData(t, 10)
	stream, err := dataGenerator.NewOrderStream(orderCount, 0, generator.ArrivalUnbounded, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	started := time.Now()
	result, err := Run(ctx, stream, &trackingMatcher{matcher: matcher, delay: 5 * time.Millisecond}, riders, Options{
		Workers:         2,
		BatchSize:       1,
		ChannelCapacity: 1,
		ExpectedOrders:  orderCount,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("Run() took too long to stop: %s", time.Since(started))
	}
	if result.CompletedOrders >= orderCount || result.UnfinishedOrders == 0 {
		t.Fatalf("cancelled result = %+v", result)
	}
	if result.Report.AssignmentCount != result.CompletedOrders || result.Report.RiderOrderCountSum != result.CompletedOrders {
		t.Fatalf("partial report does not match completed count: %+v", result)
	}
}

func TestWorkerPanicCancelsRunAndReportsOrderAndStack(t *testing.T) {
	const orderCount = 50
	riders := []model.Rider{{UID: 1}}
	source := newSliceSource(orderCount)
	panicSequence := uint64(7)

	result, err := Run(context.Background(), source, &faultMatcher{panicAt: &panicSequence}, riders, Options{
		Workers:         2,
		BatchSize:       1,
		ChannelCapacity: 2,
		ExpectedOrders:  orderCount,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want panic failure")
	}
	var panicError *PanicError
	if !errors.As(err, &panicError) {
		t.Fatalf("Run() error = %T %v, want PanicError", err, err)
	}
	if panicError.Sequence != panicSequence || panicError.OrderID != 1_000+panicSequence {
		t.Fatalf("PanicError = %+v", panicError)
	}
	if !strings.Contains(panicError.Stack, "faultMatcher).Match") {
		t.Fatalf("panic stack does not identify matcher: %s", panicError.Stack)
	}
	if result.CompletedOrders >= orderCount || result.UnfinishedOrders == 0 {
		t.Fatalf("panic result = %+v", result)
	}
}

func TestMatcherErrorCancelsWholeRun(t *testing.T) {
	const orderCount = 30
	riders := []model.Rider{{UID: 1}}
	failSequence := uint64(5)
	result, err := Run(context.Background(), newSliceSource(orderCount), &faultMatcher{errorAt: &failSequence}, riders, Options{
		Workers:         2,
		BatchSize:       2,
		ChannelCapacity: 2,
		ExpectedOrders:  orderCount,
	})
	if err == nil || !strings.Contains(err.Error(), "sequence 5") {
		t.Fatalf("Run() error = %v, want sequence-aware matcher failure", err)
	}
	if result.CompletedOrders >= orderCount {
		t.Fatalf("CompletedOrders = %d, want partial run", result.CompletedOrders)
	}
}

func TestProducerErrorCancelsWorkers(t *testing.T) {
	riders := []model.Rider{{UID: 1}}
	source := &failingSource{remaining: 5}
	result, err := Run(context.Background(), source, &faultMatcher{}, riders, Options{
		Workers:         2,
		BatchSize:       2,
		ChannelCapacity: 1,
		ExpectedOrders:  10,
	})
	if err == nil || !strings.Contains(err.Error(), "source failure") {
		t.Fatalf("Run() error = %v, want source failure", err)
	}
	if result.GeneratedOrders != 5 || result.CompletedOrders >= 10 {
		t.Fatalf("producer failure result = %+v", result)
	}
}

func TestProducerHonorsPlannedArrival(t *testing.T) {
	riders := []model.Rider{{UID: 1}}
	source := &sliceSource{orders: []model.Order{
		{ID: 1, Sequence: 0},
		{ID: 2, Sequence: 1, PlannedArrivalNs: int64(30 * time.Millisecond)},
	}}
	started := time.Now()
	result, err := Run(context.Background(), source, &faultMatcher{}, riders, Options{
		Workers:         1,
		BatchSize:       2,
		ChannelCapacity: 1,
		ExpectedOrders:  2,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("Run() elapsed = %s, producer ignored planned arrival", elapsed)
	}
	assertConservation(t, result, 2)
}

func TestRunValidatesInputs(t *testing.T) {
	riders := []model.Rider{{UID: 1}}
	validOptions := Options{Workers: 1, BatchSize: 1, ChannelCapacity: 1, ExpectedOrders: 1}
	if _, err := Run(nil, newSliceSource(1), &faultMatcher{}, riders, validOptions); err == nil {
		t.Fatal("Run(nil context) error = nil")
	}
	if _, err := Run(context.Background(), nil, &faultMatcher{}, riders, validOptions); err == nil {
		t.Fatal("Run(nil source) error = nil")
	}
	if _, err := Run(context.Background(), newSliceSource(1), nil, riders, validOptions); err == nil {
		t.Fatal("Run(nil matcher) error = nil")
	}
	if _, err := Run(context.Background(), newSliceSource(1), &faultMatcher{}, riders, Options{}); err == nil {
		t.Fatal("Run(invalid options) error = nil")
	}
}

type trackingMatcher struct {
	matcher   Matcher
	delay     time.Duration
	mu        sync.Mutex
	seen      []uint8
	duplicate bool
}

func (m *trackingMatcher) Match(order model.Order) (model.Assignment, error) {
	if m.delay != 0 {
		time.Sleep(m.delay)
	}
	if m.seen != nil {
		m.mu.Lock()
		if order.Sequence >= uint64(len(m.seen)) || m.seen[order.Sequence] != 0 {
			m.duplicate = true
		} else {
			m.seen[order.Sequence] = 1
		}
		m.mu.Unlock()
	}
	return m.matcher.Match(order)
}

func (m *trackingMatcher) assertExactlyOnce(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.duplicate {
		t.Fatal("matcher observed a duplicate or out-of-range sequence")
	}
	for sequence, count := range m.seen {
		if count != 1 {
			t.Fatalf("sequence %d observed %d times", sequence, count)
		}
	}
}

type faultMatcher struct {
	panicAt *uint64
	errorAt *uint64
}

func (m *faultMatcher) Match(order model.Order) (model.Assignment, error) {
	if m.panicAt != nil && order.Sequence == *m.panicAt {
		panic("intentional matcher panic")
	}
	if m.errorAt != nil && order.Sequence == *m.errorAt {
		return model.Assignment{}, errors.New("intentional matcher error")
	}
	return model.Assignment{
		OrderID:               order.ID,
		Sequence:              order.Sequence,
		RiderUID:              1,
		DistanceSquaredMeters: float64(order.Sequence),
	}, nil
}

type sliceSource struct {
	orders []model.Order
	next   int
}

func newSliceSource(count int) *sliceSource {
	orders := make([]model.Order, count)
	for index := range orders {
		orders[index] = model.Order{ID: 1_000 + uint64(index), Sequence: uint64(index)}
	}
	return &sliceSource{orders: orders}
}

func (s *sliceSource) Next() (model.Order, bool, error) {
	if s.next >= len(s.orders) {
		return model.Order{}, false, nil
	}
	order := s.orders[s.next]
	s.next++
	return order, true, nil
}

type failingSource struct {
	remaining int
	next      uint64
}

func (s *failingSource) Next() (model.Order, bool, error) {
	if s.remaining == 0 {
		return model.Order{}, false, errors.New("source failure")
	}
	order := model.Order{ID: s.next + 1, Sequence: s.next}
	s.next++
	s.remaining--
	return order, true, nil
}

func testData(t *testing.T, riderCount int) (generator.Generator, []model.Rider, kdtree.Matcher) {
	t.Helper()
	dataGenerator, err := generator.New(42, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("generator.New() error = %v", err)
	}
	riders, err := dataGenerator.GenerateRiders(riderCount, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	matcher, err := kdtree.New(riders)
	if err != nil {
		t.Fatalf("kdtree.New() error = %v", err)
	}
	return dataGenerator, riders, matcher
}

func serialSummary(t *testing.T, dataGenerator generator.Generator, matcher Matcher, riders []model.Rider, orderCount int) report.Summary {
	t.Helper()
	stream, err := dataGenerator.NewOrderStream(orderCount, 0, generator.ArrivalUnbounded, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	reporter, err := report.New(riders)
	if err != nil {
		t.Fatalf("report.New() error = %v", err)
	}
	for {
		order, ok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if !ok {
			break
		}
		assignment, err := matcher.Match(order)
		if err != nil {
			t.Fatalf("Match() error = %v", err)
		}
		if err := reporter.Observe(assignment); err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
	}
	return reporter.Summary()
}

func assertConservation(t *testing.T, result Result, expected uint64) {
	t.Helper()
	if result.GeneratedOrders != expected || result.AdmittedOrders != expected || result.CompletedOrders != expected || result.UnfinishedOrders != 0 {
		t.Fatalf("pipeline counts = generated %d admitted %d completed %d unfinished %d, want %d/%d/%d/0", result.GeneratedOrders, result.AdmittedOrders, result.CompletedOrders, result.UnfinishedOrders, expected, expected, expected)
	}
	if result.Report.AssignmentCount != expected || result.Report.RiderOrderCountSum != expected {
		t.Fatalf("report conservation = %+v", result.Report)
	}
}

func assertSummariesEquivalent(t *testing.T, concurrent, serial report.Summary) {
	t.Helper()
	if concurrent.AssignmentCount != serial.AssignmentCount ||
		concurrent.RiderOrderCountSum != serial.RiderOrderCountSum ||
		!reflect.DeepEqual(concurrent.Bottom10, serial.Bottom10) ||
		concurrent.ZeroRiderCount != serial.ZeroRiderCount ||
		concurrent.MinOrders != serial.MinOrders ||
		concurrent.MeanOrders != serial.MeanOrders ||
		concurrent.MaxOrders != serial.MaxOrders ||
		concurrent.VarianceOrders != serial.VarianceOrders ||
		concurrent.StdDevOrders != serial.StdDevOrders ||
		concurrent.CoefficientOfVariation != serial.CoefficientOfVariation ||
		concurrent.AverageDistanceMeters != serial.AverageDistanceMeters ||
		concurrent.P95DistanceMeters != serial.P95DistanceMeters ||
		concurrent.MaxDistanceMeters != serial.MaxDistanceMeters ||
		concurrent.DistanceHistogramOverflow != serial.DistanceHistogramOverflow {
		t.Fatalf("concurrent summary differs:\nconcurrent=%+v\nserial=%+v", concurrent, serial)
	}
}

func sum(values []uint64) uint64 {
	var total uint64
	for _, value := range values {
		total += value
	}
	return total
}
