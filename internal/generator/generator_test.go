package generator

import (
	"reflect"
	"testing"
	"time"

	"ride-sharing/internal/geo"
	"ride-sharing/internal/model"
)

func TestGeneratorIsReproducible(t *testing.T) {
	first := mustGenerator(t, 42)
	second := mustGenerator(t, 42)

	firstRiders, err := first.GenerateRiders(25, DistributionUniform)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	secondRiders, err := second.GenerateRiders(25, DistributionUniform)
	if err != nil {
		t.Fatalf("GenerateRiders() second error = %v", err)
	}
	if !reflect.DeepEqual(firstRiders, secondRiders) {
		t.Fatal("rider sequences differ for identical seeds")
	}

	firstOrders := readOrders(t, first, 50, ArrivalUniformWindow, DistributionHotspot)
	secondOrders := readOrders(t, second, 50, ArrivalUniformWindow, DistributionHotspot)
	if !reflect.DeepEqual(firstOrders, secondOrders) {
		t.Fatal("order sequences differ for identical seeds")
	}
}

func TestRiderAndOrderRandomStreamsAreIndependent(t *testing.T) {
	generator := mustGenerator(t, 123)
	baseline := readOrders(t, generator, 20, ArrivalUniformWindow, DistributionUniform)

	if _, err := generator.GenerateRiders(1_000, DistributionHotspot); err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	afterRiderChange := readOrders(t, generator, 20, ArrivalUniformWindow, DistributionUniform)

	if !reflect.DeepEqual(baseline, afterRiderChange) {
		t.Fatal("generating riders changed the independent order stream")
	}
}

func TestGeneratedDataStaysInsideBoundsAndKeepsProjectedPoint(t *testing.T) {
	generator := mustGenerator(t, 7)
	projector, err := geo.NewProjector(geo.SanFranciscoBounds.Center())
	if err != nil {
		t.Fatalf("NewProjector() error = %v", err)
	}

	for _, distribution := range []SpatialDistribution{DistributionUniform, DistributionHotspot, DistributionSkewed} {
		riders, err := generator.GenerateRiders(100, distribution)
		if err != nil {
			t.Fatalf("GenerateRiders(%q) error = %v", distribution, err)
		}
		for _, rider := range riders {
			assertPointAndProjection(t, projector, rider.Location, rider.Point)
		}

		orders := readOrdersWithDistribution(t, generator, 100, ArrivalUniformWindow, distribution)
		for _, order := range orders {
			assertPointAndProjection(t, projector, order.Pickup, order.Point)
		}
	}
}

func TestOrderSequenceAndIDsAreUnique(t *testing.T) {
	generator := mustGenerator(t, 99)
	orders := readOrders(t, generator, 1_000, ArrivalUnbounded, DistributionUniform)
	ids := make(map[uint64]struct{}, len(orders))

	for index, order := range orders {
		if order.Sequence != uint64(index) {
			t.Fatalf("order %d Sequence = %d", index, order.Sequence)
		}
		if _, exists := ids[order.ID]; exists {
			t.Fatalf("duplicate order ID %d", order.ID)
		}
		ids[order.ID] = struct{}{}
	}
}

func TestArrivalModels(t *testing.T) {
	generator := mustGenerator(t, 42)
	window := 10 * time.Second

	uniform := readOrdersInWindow(t, generator, 6, window, ArrivalUniformWindow)
	if uniform[0].PlannedArrivalNs != 0 || uniform[len(uniform)-1].PlannedArrivalNs > window.Nanoseconds() {
		t.Fatalf("uniform arrivals = first %d, last %d", uniform[0].PlannedArrivalNs, uniform[len(uniform)-1].PlannedArrivalNs)
	}
	assertNonDecreasing(t, uniform)

	burst := readOrdersInWindow(t, generator, 6, window, ArrivalFrontLoadedBurst)
	if burst[4].PlannedArrivalNs != (2 * time.Second).Nanoseconds() {
		t.Fatalf("80%% burst arrival = %s, want 2s", time.Duration(burst[4].PlannedArrivalNs))
	}
	if burst[len(burst)-1].PlannedArrivalNs != window.Nanoseconds() {
		t.Fatalf("burst last arrival = %s, want %s", time.Duration(burst[len(burst)-1].PlannedArrivalNs), window)
	}
	assertNonDecreasing(t, burst)

	unbounded := readOrdersInWindow(t, generator, 6, window, ArrivalUnbounded)
	for _, order := range unbounded {
		if order.PlannedArrivalNs != 0 {
			t.Fatalf("unbounded PlannedArrivalNs = %d, want 0", order.PlannedArrivalNs)
		}
	}
}

func TestOrderStreamDoesNotRetainAllOrders(t *testing.T) {
	generator := mustGenerator(t, 42)
	stream, err := generator.NewOrderStream(10_000_000, 10*time.Minute, ArrivalUniformWindow, DistributionUniform)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}

	first, ok, err := stream.Next()
	if err != nil || !ok {
		t.Fatalf("Next() = (%+v, %t, %v)", first, ok, err)
	}
	if first.Sequence != 0 {
		t.Fatalf("first Sequence = %d", first.Sequence)
	}
}

func mustGenerator(t *testing.T, seed int64) Generator {
	t.Helper()
	generator, err := New(seed, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return generator
}

func readOrders(t *testing.T, generator Generator, count int, arrival ArrivalModel, distribution SpatialDistribution) []model.Order {
	t.Helper()
	return readOrdersInWindowWithDistribution(t, generator, count, 30*time.Second, arrival, distribution)
}

func readOrdersWithDistribution(t *testing.T, generator Generator, count int, arrival ArrivalModel, distribution SpatialDistribution) []model.Order {
	t.Helper()
	return readOrdersInWindowWithDistribution(t, generator, count, 30*time.Second, arrival, distribution)
}

func readOrdersInWindow(t *testing.T, generator Generator, count int, window time.Duration, arrival ArrivalModel) []model.Order {
	t.Helper()
	return readOrdersInWindowWithDistribution(t, generator, count, window, arrival, DistributionUniform)
}

func readOrdersInWindowWithDistribution(t *testing.T, generator Generator, count int, window time.Duration, arrival ArrivalModel, distribution SpatialDistribution) []model.Order {
	t.Helper()
	stream, err := generator.NewOrderStream(count, window, arrival, distribution)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	orders := make([]model.Order, 0, count)
	for {
		order, ok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if !ok {
			break
		}
		orders = append(orders, order)
	}
	return orders
}

func assertPointAndProjection(t *testing.T, projector geo.Projector, location model.GeoPoint, point model.Point2D) {
	t.Helper()
	if !geo.SanFranciscoBounds.Contains(location) {
		t.Fatalf("location %+v is outside bounds", location)
	}
	want, err := projector.Project(location)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if point != want {
		t.Fatalf("projected point = %+v, want %+v", point, want)
	}
}

func assertNonDecreasing(t *testing.T, orders []model.Order) {
	t.Helper()
	for index := 1; index < len(orders); index++ {
		if orders[index].PlannedArrivalNs < orders[index-1].PlannedArrivalNs {
			t.Fatalf("arrival %d (%d) is before arrival %d (%d)", index, orders[index].PlannedArrivalNs, index-1, orders[index-1].PlannedArrivalNs)
		}
	}
}
