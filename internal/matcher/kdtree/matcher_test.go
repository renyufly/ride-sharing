package kdtree

import (
	"math"
	"reflect"
	"testing"
	"time"

	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/matcher/bruteforce"
	"ride-sharing/internal/model"
)

func TestMatchReturnsGlobalNearestRider(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{
		{UID: 20, Point: model.Point2D{X: 10, Y: 10}},
		{UID: 30, Point: model.Point2D{X: 2, Y: 3}},
		{UID: 10, Point: model.Point2D{X: -5, Y: -5}},
	})
	order := model.Order{ID: 1001, Sequence: 7, Point: model.Point2D{X: 1, Y: 1}}

	assignment, err := matcher.Match(order)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 30 || assignment.DistanceSquaredMeters != 5 {
		t.Fatalf("Match() = %+v, want rider 30 at squared distance 5", assignment)
	}
	if assignment.OrderID != order.ID || assignment.Sequence != order.Sequence {
		t.Fatalf("Match() identity = %+v, want order identity %+v", assignment, order)
	}
}

func TestMatchDoesNotPruneSmallerUIDAtEqualDistance(t *testing.T) {
	matcher := mustMatcher(t, []model.Rider{
		{UID: 90, Point: model.Point2D{X: 0, Y: 1}},
		{UID: 70, Point: model.Point2D{X: 1, Y: 0}},
		{UID: 3, Point: model.Point2D{X: -1, Y: 0}},
		{UID: 50, Point: model.Point2D{X: 0, Y: -1}},
	})

	assignment, err := matcher.Match(model.Order{Point: model.Point2D{}})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 3 {
		t.Fatalf("Match() RiderUID = %d, want smallest tied UID 3", assignment.RiderUID)
	}
}

func TestDuplicateCoordinatesAndDegenerateDistribution(t *testing.T) {
	riders := make([]model.Rider, 127)
	for index := range riders {
		riders[index] = model.Rider{
			UID:   uint64(127 - index),
			Point: model.Point2D{X: 12.5, Y: -8.25},
		}
	}
	matcher := mustMatcher(t, riders)

	assignment, err := matcher.Match(model.Order{Point: model.Point2D{X: 12.5, Y: -8.25}})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 1 || assignment.DistanceSquaredMeters != 0 {
		t.Fatalf("Match() = %+v, want UID 1 at zero distance", assignment)
	}
	if height := treeHeight(matcher.nodes, matcher.root); height > 7 {
		t.Fatalf("degenerate coordinate tree height = %d, want at most 7", height)
	}
}

func TestMatchesBruteForceForGeneratedScenarios(t *testing.T) {
	distributions := []generator.SpatialDistribution{
		generator.DistributionUniform,
		generator.DistributionHotspot,
		generator.DistributionSkewed,
	}
	for _, riderDistribution := range distributions {
		for _, orderDistribution := range distributions {
			name := string(riderDistribution) + " riders / " + string(orderDistribution) + " orders"
			t.Run(name, func(t *testing.T) {
				compareGeneratedMatchers(t, 100, 500, riderDistribution, orderDistribution)
			})
		}
	}
}

func TestMatchesBruteForceAtRequiredBaselineScale(t *testing.T) {
	compareGeneratedMatchers(t, 100, 10_000, generator.DistributionUniform, generator.DistributionUniform)
}

func TestTopKMatchesBruteForceAndIsStable(t *testing.T) {
	dataGenerator, err := generator.New(91, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("generator.New() error = %v", err)
	}
	riders, err := dataGenerator.GenerateRiders(127, generator.DistributionHotspot)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	baseline, err := bruteforce.New(riders)
	if err != nil {
		t.Fatalf("bruteforce.New() error = %v", err)
	}
	tree := mustMatcher(t, riders)
	stream, err := dataGenerator.NewOrderStream(500, 0, generator.ArrivalUnbounded, generator.DistributionSkewed)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}
	for {
		order, ok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if !ok {
			break
		}
		for _, limit := range []int{1, 4, 8, 32, len(riders), len(riders) + 10} {
			want, err := baseline.TopK(order, limit)
			if err != nil {
				t.Fatalf("bruteforce.TopK(sequence=%d, limit=%d) error = %v", order.Sequence, limit, err)
			}
			got, err := tree.TopK(order, limit)
			if err != nil {
				t.Fatalf("kdtree.TopK(sequence=%d, limit=%d) error = %v", order.Sequence, limit, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("sequence %d limit %d: kdtree TopK = %+v, brute-force = %+v", order.Sequence, limit, got, want)
			}
		}
	}
}

func TestTopKBreaksEqualDistanceByUID(t *testing.T) {
	riders := []model.Rider{
		{UID: 9, Point: model.Point2D{X: 1}},
		{UID: 3, Point: model.Point2D{X: -1}},
		{UID: 7, Point: model.Point2D{Y: 1}},
		{UID: 1, Point: model.Point2D{Y: -1}},
	}
	candidates, err := mustMatcher(t, riders).TopK(model.Order{}, 3)
	if err != nil {
		t.Fatalf("TopK() error = %v", err)
	}
	for index, wantUID := range []uint64{1, 3, 7} {
		if candidates[index].RiderUID != wantUID {
			t.Fatalf("candidate %d UID = %d, want %d; candidates=%+v", index, candidates[index].RiderUID, wantUID, candidates)
		}
	}
}

func TestMatchesBruteForceOnBoundaryOrders(t *testing.T) {
	dataGenerator, err := generator.New(42, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("generator.New() error = %v", err)
	}
	riders, err := dataGenerator.GenerateRiders(100, generator.DistributionUniform)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	baseline, err := bruteforce.New(riders)
	if err != nil {
		t.Fatalf("bruteforce.New() error = %v", err)
	}
	tree := mustMatcher(t, riders)
	projector, err := geo.NewProjector(geo.SanFranciscoBounds.Center())
	if err != nil {
		t.Fatalf("geo.NewProjector() error = %v", err)
	}

	locations := []model.GeoPoint{
		{Latitude: geo.SanFranciscoBounds.MinLatitude, Longitude: geo.SanFranciscoBounds.MinLongitude},
		{Latitude: geo.SanFranciscoBounds.MinLatitude, Longitude: geo.SanFranciscoBounds.MaxLongitude},
		{Latitude: geo.SanFranciscoBounds.MaxLatitude, Longitude: geo.SanFranciscoBounds.MinLongitude},
		{Latitude: geo.SanFranciscoBounds.MaxLatitude, Longitude: geo.SanFranciscoBounds.MaxLongitude},
	}
	for sequence, location := range locations {
		point, err := projector.Project(location)
		if err != nil {
			t.Fatalf("Project() error = %v", err)
		}
		compareOne(t, baseline, tree, model.Order{ID: uint64(sequence + 1), Sequence: uint64(sequence), Pickup: location, Point: point})
	}
}

func TestTreeBoundsContainEveryNodeAndChild(t *testing.T) {
	dataGenerator, err := generator.New(55, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("generator.New() error = %v", err)
	}
	riders, err := dataGenerator.GenerateRiders(1_000, generator.DistributionSkewed)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	matcher := mustMatcher(t, riders)

	if len(matcher.nodes) != len(riders) {
		t.Fatalf("node count = %d, want %d", len(matcher.nodes), len(riders))
	}
	assertBounds(t, matcher.nodes, matcher.root)
	if height := treeHeight(matcher.nodes, matcher.root); height > 10 {
		t.Fatalf("tree height = %d, want at most 10", height)
	}
}

func TestNewRejectsInvalidRidersAndMatchRejectsInvalidOrder(t *testing.T) {
	tests := [][]model.Rider{
		nil,
		{{UID: 1}, {UID: 1}},
		{{UID: 1, Point: model.Point2D{X: math.NaN()}}},
	}
	for _, riders := range tests {
		if _, err := New(riders); err == nil {
			t.Fatalf("New(%+v) error = nil", riders)
		}
	}

	matcher := mustMatcher(t, []model.Rider{{UID: 1}})
	if _, err := matcher.Match(model.Order{ID: 9, Point: model.Point2D{Y: math.Inf(1)}}); err == nil {
		t.Fatal("Match(non-finite order) error = nil")
	}
}

func TestNewOwnsRiderCoordinates(t *testing.T) {
	riders := []model.Rider{{UID: 1, Point: model.Point2D{X: 1}}, {UID: 2, Point: model.Point2D{X: 10}}}
	matcher := mustMatcher(t, riders)
	riders[0].Point.X = 1_000

	assignment, err := matcher.Match(model.Order{Point: model.Point2D{}})
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if assignment.RiderUID != 1 {
		t.Fatalf("Match() RiderUID = %d, want copied rider 1", assignment.RiderUID)
	}
}

func compareGeneratedMatchers(t *testing.T, riderCount, orderCount int, riderDistribution, orderDistribution generator.SpatialDistribution) {
	t.Helper()
	dataGenerator, err := generator.New(42, geo.SanFranciscoBounds)
	if err != nil {
		t.Fatalf("generator.New() error = %v", err)
	}
	riders, err := dataGenerator.GenerateRiders(riderCount, riderDistribution)
	if err != nil {
		t.Fatalf("GenerateRiders() error = %v", err)
	}
	baseline, err := bruteforce.New(riders)
	if err != nil {
		t.Fatalf("bruteforce.New() error = %v", err)
	}
	tree := mustMatcher(t, riders)
	stream, err := dataGenerator.NewOrderStream(orderCount, 30*time.Second, generator.ArrivalUniformWindow, orderDistribution)
	if err != nil {
		t.Fatalf("NewOrderStream() error = %v", err)
	}

	for {
		order, ok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if !ok {
			break
		}
		compareOne(t, baseline, tree, order)
	}
}

func compareOne(t *testing.T, baseline bruteforce.Matcher, tree Matcher, order model.Order) {
	t.Helper()
	want, err := baseline.Match(order)
	if err != nil {
		t.Fatalf("bruteforce.Match(sequence=%d) error = %v", order.Sequence, err)
	}
	got, err := tree.Match(order)
	if err != nil {
		t.Fatalf("kdtree.Match(sequence=%d) error = %v", order.Sequence, err)
	}
	if got != want {
		t.Fatalf("sequence %d: kdtree = %+v, brute-force = %+v", order.Sequence, got, want)
	}
}

func mustMatcher(t *testing.T, riders []model.Rider) Matcher {
	t.Helper()
	matcher, err := New(riders)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return matcher
}

func assertBounds(t *testing.T, nodes []node, nodeIndex int) {
	t.Helper()
	if nodeIndex == missingNode {
		return
	}
	current := nodes[nodeIndex]
	if current.rider.Point.X < current.minX || current.rider.Point.X > current.maxX ||
		current.rider.Point.Y < current.minY || current.rider.Point.Y > current.maxY {
		t.Fatalf("node %d rider %+v outside bounds %+v", nodeIndex, current.rider.Point, current)
	}
	for _, childIndex := range []int{current.left, current.right} {
		if childIndex == missingNode {
			continue
		}
		child := nodes[childIndex]
		if child.minX < current.minX || child.maxX > current.maxX || child.minY < current.minY || child.maxY > current.maxY {
			t.Fatalf("child %d bounds escape parent %d", childIndex, nodeIndex)
		}
		assertBounds(t, nodes, childIndex)
	}
}

func treeHeight(nodes []node, nodeIndex int) int {
	if nodeIndex == missingNode {
		return 0
	}
	return 1 + max(treeHeight(nodes, nodes[nodeIndex].left), treeHeight(nodes, nodes[nodeIndex].right))
}
