package runsetup

import (
	"fmt"
	"io"
	"time"

	"ride-sharing/internal/adapter/deferred/file"
	"ride-sharing/internal/adapter/simulation"
	"ride-sharing/internal/adapter/simulation/orders"
	"ride-sharing/internal/adapter/simulation/riders"
	"ride-sharing/internal/adapter/spatial/bruteforce"
	"ride-sharing/internal/adapter/spatial/kdtree"
	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/app/run"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/rider"
	"ride-sharing/internal/platform/config"
)

// Factory assembles the existing simulated input and static spatial index.
type Factory struct{}

func (Factory) Prepare(cfg config.Config, providedSource appmatching.OrderSource) (run.Prepared, error) {
	riderStarted := time.Now()
	scenario, err := simulation.New(cfg.Seed, geo.SanFranciscoBounds)
	if err != nil {
		return run.Prepared{}, err
	}
	riderSet, err := riders.Generate(scenario, cfg.RiderCount, cfg.RiderDistribution)
	if err != nil {
		return run.Prepared{}, fmt.Errorf("generate riders: %w", err)
	}
	riderDuration := time.Since(riderStarted)

	indexStarted := time.Now()
	matcher, err := createMatcher(cfg, riderSet)
	if err != nil {
		return run.Prepared{}, err
	}
	indexDuration := time.Since(indexStarted)

	source, expected, closer, err := createOrderSource(&cfg, scenario, providedSource)
	if err != nil {
		return run.Prepared{}, fmt.Errorf("create order source: %w", err)
	}
	seeds := scenario.Seeds()
	return run.Prepared{
		Config: cfg, Bounds: scenario.Bounds(), ProjectionOrigin: scenario.Origin(),
		Seeds:  run.Seeds{Rider: seeds.Rider, Order: seeds.Order},
		Riders: riderSet, Matcher: matcher, OrderSource: source,
		ExpectedOrders: expected, SourceCloser: closer,
		RiderGeneration: riderDuration, IndexBuild: indexDuration,
	}, nil
}

func createMatcher(cfg config.Config, riderSet []rider.Rider) (run.Matcher, error) {
	switch cfg.Algorithm {
	case config.AlgorithmBruteForce:
		return bruteforce.New(riderSet)
	case config.AlgorithmKDTree:
		return kdtree.New(riderSet)
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", cfg.Algorithm)
	}
}

func createOrderSource(cfg *config.Config, scenario simulation.Scenario, provided appmatching.OrderSource) (appmatching.OrderSource, uint64, io.Closer, error) {
	if provided != nil {
		if cfg.DeferredInput != "" {
			return nil, 0, nil, fmt.Errorf("order source and deferred input cannot both be set")
		}
		return provided, uint64(cfg.OrderCount), nil, nil
	}
	if cfg.DeferredInput == "" {
		stream, err := orders.NewStream(scenario, cfg.OrderCount, cfg.ArrivalWindow, cfg.ArrivalModel, cfg.OrderDistribution)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("create generated order stream: %w", err)
		}
		return stream, uint64(cfg.OrderCount), nil, nil
	}
	if cfg.Attempt <= 1 {
		return nil, 0, nil, fmt.Errorf("deferred input requires attempt greater than one")
	}
	source, count, err := file.OpenFileSource(cfg.DeferredInput, uint32(cfg.Attempt-1))
	if err != nil {
		return nil, 0, nil, err
	}
	cfg.OrderCount = int(count)
	return source, count, source, nil
}
