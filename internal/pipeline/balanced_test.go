package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/matcher/kdtree"
	"ride-sharing/internal/model"
)

func TestBalancedCoordinatorUsesLoadThenDistanceThenUID(t *testing.T) {
	riders := []model.Rider{
		{UID: 1, Point: model.Point2D{X: 0}},
		{UID: 2, Point: model.Point2D{X: 10}},
	}
	matcher, err := kdtree.New(riders)
	if err != nil {
		t.Fatalf("kdtree.New() error = %v", err)
	}
	orders := []model.Order{
		{ID: 10, Sequence: 0, Point: model.Point2D{X: 0}},
		{ID: 11, Sequence: 1, Point: model.Point2D{X: 0}},
		{ID: 12, Sequence: 2, Point: model.Point2D{X: 0}},
	}
	result, err := RunBalanced(context.Background(), &sliceSource{orders: orders}, matcher, riders, BalancedOptions{
		Options: Options{Workers: 2, BatchSize: 1, ChannelCapacity: 2, ExpectedOrders: 3, PreviewSize: 3},
		TopK:    2, MaxExtraDistanceMeters: 20,
	})
	if err != nil {
		t.Fatalf("RunBalanced() error = %v", err)
	}
	wantUIDs := []uint64{1, 2, 1}
	for index, wantUID := range wantUIDs {
		if result.AssignmentPreview[index].RiderUID != wantUID {
			t.Fatalf("assignment %d rider = %d, want %d; assignments=%+v", index, result.AssignmentPreview[index].RiderUID, wantUID, result.AssignmentPreview)
		}
	}
	assertConservation(t, result, 3)
}

func TestBalancedRespectsMaximumExtraDistance(t *testing.T) {
	riders := []model.Rider{
		{UID: 1, Point: model.Point2D{X: 0}},
		{UID: 2, Point: model.Point2D{X: 100}},
	}
	matcher, err := kdtree.New(riders)
	if err != nil {
		t.Fatalf("kdtree.New() error = %v", err)
	}
	result, err := RunBalanced(context.Background(), newSliceSource(10), matcher, riders, BalancedOptions{
		Options: Options{Workers: 2, BatchSize: 1, ChannelCapacity: 2, ExpectedOrders: 10, PreviewSize: 10},
		TopK:    2, MaxExtraDistanceMeters: 10,
	})
	if err != nil {
		t.Fatalf("RunBalanced() error = %v", err)
	}
	for _, assignment := range result.AssignmentPreview {
		if assignment.RiderUID != 1 {
			t.Fatalf("assignment selected rider %d outside distance cap", assignment.RiderUID)
		}
	}
}

func TestBalancedReordersCandidateResultsDeterministically(t *testing.T) {
	riders := []model.Rider{
		{UID: 1, Point: model.Point2D{X: 0}},
		{UID: 2, Point: model.Point2D{X: 1}},
		{UID: 3, Point: model.Point2D{X: 2}},
	}
	base, err := kdtree.New(riders)
	if err != nil {
		t.Fatalf("kdtree.New() error = %v", err)
	}
	orders := make([]model.Order, 60)
	for index := range orders {
		orders[index] = model.Order{ID: uint64(100 + index), Sequence: uint64(index), Point: model.Point2D{X: 0.5}}
	}
	options := BalancedOptions{
		Options: Options{Workers: 4, BatchSize: 1, ChannelCapacity: 4, ExpectedOrders: uint64(len(orders)), PreviewSize: len(orders)},
		TopK:    3, MaxExtraDistanceMeters: 10,
	}

	first, err := RunBalanced(context.Background(), &sliceSource{orders: append([]model.Order(nil), orders...)}, delayedCandidateMatcher{matcher: base}, riders, options)
	if err != nil {
		t.Fatalf("first RunBalanced() error = %v", err)
	}
	second, err := RunBalanced(context.Background(), &sliceSource{orders: append([]model.Order(nil), orders...)}, delayedCandidateMatcher{matcher: base}, riders, options)
	if err != nil {
		t.Fatalf("second RunBalanced() error = %v", err)
	}
	if !reflect.DeepEqual(first.AssignmentPreview, second.AssignmentPreview) || !reflect.DeepEqual(first.Report, second.Report) {
		t.Fatalf("balanced runs differ:\nfirst=%+v\nsecond=%+v", first.AssignmentPreview, second.AssignmentPreview)
	}
	if first.StrategyDetails == nil || first.StrategyDetails.MaxReorderDepth <= 1 {
		t.Fatalf("reorder metrics = %+v, want observed out-of-order depth", first.StrategyDetails)
	}
}

func TestBalancedLargeBatchesCannotStarveNextSequence(t *testing.T) {
	dataGenerator, riders, matcher := testData(t, 1_000)
	stream, err := dataGenerator.NewOrderStream(100_000, 0, "unbounded", "uniform")
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := RunBalanced(ctx, stream, matcher, riders, BalancedOptions{
		Options: Options{Workers: 2, BatchSize: 256, ChannelCapacity: 16, ExpectedOrders: 100_000},
		TopK:    8, MaxExtraDistanceMeters: 300,
	})
	if err != nil {
		t.Fatalf("RunBalanced() error = %v", err)
	}
	assertConservation(t, result, 100_000)
	if result.StrategyDetails.MaxReorderDepth > result.StrategyDetails.ReorderWindow {
		t.Fatalf("reorder depth %d exceeds window %d", result.StrategyDetails.MaxReorderDepth, result.StrategyDetails.ReorderWindow)
	}
}

func TestBalancedRunDoesNotChangeNearestStrategy(t *testing.T) {
	dataGenerator, riders, matcher := testData(t, 100)
	nearestBeforeStream, err := dataGenerator.NewOrderStream(10_000, 0, "unbounded", "uniform")
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	nearestOptions := Options{Workers: 2, BatchSize: 31, ChannelCapacity: 4, ExpectedOrders: 10_000, PreviewSize: 3}
	before, err := Run(context.Background(), nearestBeforeStream, matcher, riders, nearestOptions)
	if err != nil {
		t.Fatalf("nearest before error = %v", err)
	}

	balancedStream, _ := dataGenerator.NewOrderStream(10_000, 0, "unbounded", "uniform")
	if _, err := RunBalanced(context.Background(), balancedStream, matcher, riders, BalancedOptions{
		Options: nearestOptions, TopK: 8, MaxExtraDistanceMeters: 300,
	}); err != nil {
		t.Fatalf("balanced run error = %v", err)
	}

	nearestAfterStream, _ := dataGenerator.NewOrderStream(10_000, 0, "unbounded", "uniform")
	after, err := Run(context.Background(), nearestAfterStream, matcher, riders, nearestOptions)
	if err != nil {
		t.Fatalf("nearest after error = %v", err)
	}
	if !reflect.DeepEqual(before.AssignmentPreview, after.AssignmentPreview) || !reflect.DeepEqual(before.Report, after.Report) {
		t.Fatalf("nearest strategy changed after balanced run:\nbefore=%+v\nafter=%+v", before.Report, after.Report)
	}
}

func TestBalancedCancellationStopsAllStages(t *testing.T) {
	riders := []model.Rider{{UID: 1}}
	base, err := kdtree.New(riders)
	if err != nil {
		t.Fatalf("kdtree.New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := RunBalanced(ctx, newSliceSource(1_000), slowCandidateMatcher{matcher: base, delay: 2 * time.Millisecond}, riders, BalancedOptions{
		Options: Options{Workers: 2, BatchSize: 1, ChannelCapacity: 2, ExpectedOrders: 1_000},
		TopK:    1,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunBalanced() error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("RunBalanced() cancellation took too long")
	}
	if result.CompletedOrders >= 1_000 || result.UnfinishedOrders == 0 {
		t.Fatalf("cancelled result = %+v", result)
	}
}

func TestBalancedCandidatePanicIsReported(t *testing.T) {
	riders := []model.Rider{{UID: 1}}
	result, err := RunBalanced(context.Background(), newSliceSource(20), panicCandidateMatcher{panicSequence: 5}, riders, BalancedOptions{
		Options: Options{Workers: 2, BatchSize: 1, ChannelCapacity: 2, ExpectedOrders: 20},
		TopK:    1,
	})
	if err == nil {
		t.Fatal("RunBalanced() error = nil, want candidate panic")
	}
	var panicError *CandidatePanicError
	if !errors.As(err, &panicError) || panicError.Sequence != 5 {
		t.Fatalf("RunBalanced() error = %T %v, want sequence-5 CandidatePanicError; result=%+v", err, err, result)
	}
}

type delayedCandidateMatcher struct {
	matcher kdtree.Matcher
}

type slowCandidateMatcher struct {
	matcher kdtree.Matcher
	delay   time.Duration
}

func (m slowCandidateMatcher) TopKInto(order model.Order, limit int, destination []matchrule.Candidate) ([]matchrule.Candidate, error) {
	time.Sleep(m.delay)
	return m.matcher.TopKInto(order, limit, destination)
}

type panicCandidateMatcher struct {
	panicSequence uint64
}

func (m panicCandidateMatcher) TopKInto(order model.Order, _ int, destination []matchrule.Candidate) ([]matchrule.Candidate, error) {
	if order.Sequence == m.panicSequence {
		panic("candidate boom")
	}
	return append(destination[:0], matchrule.Candidate{RiderUID: 1}), nil
}

func (m delayedCandidateMatcher) TopKInto(order model.Order, limit int, destination []matchrule.Candidate) ([]matchrule.Candidate, error) {
	if order.Sequence%7 == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	return m.matcher.TopKInto(order, limit, destination)
}
