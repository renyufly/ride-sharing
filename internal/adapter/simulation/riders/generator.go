package riders

import (
	"errors"
	"fmt"
	"math/rand"
	"ride-sharing/internal/adapter/simulation"
	"ride-sharing/internal/domain/rider"
	"ride-sharing/internal/platform/config"
)

// 一次生成所有骑手
func Generate(scenario simulation.Scenario, count int, distribution config.SpatialDistribution) ([]rider.Rider, error) {
	if count <= 0 {
		return nil, errors.New("rider count must be greater than zero")
	}
	if err := config.ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	random := rand.New(rand.NewSource(scenario.Seeds().Rider))
	riders := make([]rider.Rider, count)

	// 这里骑手数量相比千万订单小很多，
	// 而且后面的匹配算法本来就需要长期持有骑手数据，
	// 所以一次性放内存是合理的

	for index := range riders {
		location := simulation.SamplePoint(random, scenario.Bounds(), distribution)
		point, err := scenario.Projector().Project(location)
		if err != nil {
			return nil, fmt.Errorf("project rider %d: %w", index+1, err)
		}
		riders[index] = rider.Rider{
			UID:      uint64(index + 1),
			Location: location,
			Point:    point,
		}
	}
	return riders, nil
}
