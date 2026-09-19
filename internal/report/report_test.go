package report

import (
	"math"
	"reflect"
	"testing"

	"ride-sharing/internal/model"
)

func TestSummaryIncludesZeroRidersAndSortedBottom10(t *testing.T) {
	riders := makeRiders(12)
	accumulator := mustAccumulator(t, riders)

	// UID 1 and 2 remain at zero. UID n receives n-2 orders.
	for uid := uint64(3); uid <= 12; uid++ {
		for count := uint64(0); count < uid-2; count++ {
			observe(t, accumulator, uid, 1)
		}
	}

	summary := accumulator.Summary()
	want := []RiderOrderCount{
		{RiderUID: 1, OrderCount: 0},
		{RiderUID: 2, OrderCount: 0},
		{RiderUID: 3, OrderCount: 1},
		{RiderUID: 4, OrderCount: 2},
		{RiderUID: 5, OrderCount: 3},
		{RiderUID: 6, OrderCount: 4},
		{RiderUID: 7, OrderCount: 5},
		{RiderUID: 8, OrderCount: 6},
		{RiderUID: 9, OrderCount: 7},
		{RiderUID: 10, OrderCount: 8},
	}
	if !reflect.DeepEqual(summary.Bottom10, want) {
		t.Fatalf("Bottom10 = %+v, want %+v", summary.Bottom10, want)
	}
	if summary.ZeroRiderCount != 2 {
		t.Fatalf("ZeroRiderCount = %d, want 2", summary.ZeroRiderCount)
	}
}

func TestBottom10UsesUIDAsTieBreaker(t *testing.T) {
	riders := []model.Rider{{UID: 30}, {UID: 10}, {UID: 20}}
	accumulator := mustAccumulator(t, riders)

	want := []RiderOrderCount{
		{RiderUID: 10, OrderCount: 0},
		{RiderUID: 20, OrderCount: 0},
		{RiderUID: 30, OrderCount: 0},
	}
	if got := accumulator.Summary().Bottom10; !reflect.DeepEqual(got, want) {
		t.Fatalf("Bottom10 = %+v, want %+v", got, want)
	}
}

func TestAssignmentDistributionStatistics(t *testing.T) {
	accumulator := mustAccumulator(t, makeRiders(3))
	observe(t, accumulator, 2, 3)
	observe(t, accumulator, 3, 4)
	observe(t, accumulator, 3, 0)

	summary := accumulator.Summary()
	if summary.AssignmentCount != 3 || summary.RiderOrderCountSum != 3 {
		t.Fatalf("count conservation = assignments %d, rider sum %d", summary.AssignmentCount, summary.RiderOrderCountSum)
	}
	if summary.MinOrders != 0 || summary.MeanOrders != 1 || summary.MaxOrders != 2 {
		t.Fatalf("min/mean/max = %d/%f/%d", summary.MinOrders, summary.MeanOrders, summary.MaxOrders)
	}
	wantVariance := 2.0 / 3.0
	assertNear(t, summary.VarianceOrders, wantVariance)
	assertNear(t, summary.StdDevOrders, math.Sqrt(wantVariance))
	assertNear(t, summary.CoefficientOfVariation, math.Sqrt(wantVariance))
	assertNear(t, summary.AverageDistanceMeters, 7.0/3.0)
	if summary.P95DistanceMeters != 4 {
		t.Fatalf("P95DistanceMeters = %f, want 4", summary.P95DistanceMeters)
	}
	if summary.MaxDistanceMeters != 4 {
		t.Fatalf("MaxDistanceMeters = %f, want 4", summary.MaxDistanceMeters)
	}
}

func TestEmptyAssignmentSummary(t *testing.T) {
	accumulator := mustAccumulator(t, makeRiders(2))
	summary := accumulator.Summary()

	if summary.AssignmentCount != 0 || summary.RiderOrderCountSum != 0 || summary.ZeroRiderCount != 2 {
		t.Fatalf("empty summary = %+v", summary)
	}
	if summary.CoefficientOfVariation != 0 || summary.AverageDistanceMeters != 0 || summary.P95DistanceMeters != 0 {
		t.Fatalf("empty derived metrics = %+v", summary)
	}
}

func TestDistanceHistogramOverflowIsVisible(t *testing.T) {
	accumulator := mustAccumulator(t, makeRiders(1))
	observe(t, accumulator, 1, 60_000)

	summary := accumulator.Summary()
	if summary.DistanceHistogramOverflow != 1 {
		t.Fatalf("DistanceHistogramOverflow = %d, want 1", summary.DistanceHistogramOverflow)
	}
	if summary.P95DistanceMeters != 60_000 {
		t.Fatalf("P95DistanceMeters = %f, want conservative maximum 60000", summary.P95DistanceMeters)
	}
}

func TestObserveRejectsInvalidAssignment(t *testing.T) {
	accumulator := mustAccumulator(t, makeRiders(1))
	tests := []model.Assignment{
		{RiderUID: 999, DistanceSquaredMeters: 1},
		{RiderUID: 1, DistanceSquaredMeters: -1},
		{RiderUID: 1, DistanceSquaredMeters: math.NaN()},
		{RiderUID: 1, DistanceSquaredMeters: math.Inf(1)},
	}
	for _, assignment := range tests {
		if err := accumulator.Observe(assignment); err == nil {
			t.Fatalf("Observe(%+v) error = nil", assignment)
		}
	}
	if summary := accumulator.Summary(); summary.AssignmentCount != 0 {
		t.Fatalf("invalid assignments changed count to %d", summary.AssignmentCount)
	}
}

func TestNewRejectsInvalidRiderSets(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) error = nil")
	}
	if _, err := New([]model.Rider{{UID: 1}, {UID: 1}}); err == nil {
		t.Fatal("New(duplicate UID) error = nil")
	}
}

func makeRiders(count int) []model.Rider {
	riders := make([]model.Rider, count)
	for index := range riders {
		riders[index].UID = uint64(index + 1)
	}
	return riders
}

func mustAccumulator(t *testing.T, riders []model.Rider) *Accumulator {
	t.Helper()
	accumulator, err := New(riders)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return accumulator
}

func observe(t *testing.T, accumulator *Accumulator, riderUID uint64, distanceMeters float64) {
	t.Helper()
	err := accumulator.Observe(model.Assignment{
		RiderUID:              riderUID,
		DistanceSquaredMeters: distanceMeters * distanceMeters,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %f, want %f", got, want)
	}
}
