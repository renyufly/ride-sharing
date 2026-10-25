package simulation

import (
	"fmt"
	"math/rand"
	"ride-sharing/internal/domain/geo"
)

type SpatialDistribution string // 骑手/订单在哪里
// Uniform：完全随机分布
// Hotspot：热点区域 (很符合网约车场景)
// Skewed：偏斜分布 (极端不均衡的地理分布)
const (
	DistributionUniform SpatialDistribution = "uniform"
	DistributionHotspot SpatialDistribution = "hotspot"
	DistributionSkewed  SpatialDistribution = "skewed"
)

// 预先定义三个热点 (地图内部的相对位置)
var hotspotCenters = [...]struct {
	latitudeRatio  float64
	longitudeRatio float64
}{
	{latitudeRatio: 0.25, longitudeRatio: 0.30},
	{latitudeRatio: 0.70, longitudeRatio: 0.65},
	{latitudeRatio: 0.50, longitudeRatio: 0.80},
}

type Seeds struct {
	Rider int64
	Order int64
}

type Scenario struct {
	bounds    geo.BoundingBox
	projector geo.Projector
	seeds     Seeds
}

func New(masterSeed int64, bounds geo.BoundingBox) (Scenario, error) {
	if err := bounds.Validate(); err != nil {
		return Scenario{}, fmt.Errorf("invalid generator bounds: %w", err)
	}
	projector, err := geo.NewProjector(bounds.Center())
	if err != nil {
		return Scenario{}, err
	}

	return Scenario{
		bounds:    bounds,
		projector: projector,
		seeds: Seeds{
			Rider: int64(Mix64(uint64(masterSeed) ^ 0x7269646572736565)),
			Order: int64(Mix64(uint64(masterSeed) ^ 0x6f72646572736565)),
		},
	}, nil
}

func (g Scenario) Bounds() geo.BoundingBox {
	return g.bounds
}

func (g Scenario) Origin() geo.GeoPoint {
	return g.projector.Origin()
}

func (g Scenario) Seeds() Seeds {
	return g.seeds
}

func (g Scenario) Projector() geo.Projector { return g.projector }

func ValidateSpatialDistribution(distribution SpatialDistribution) error {
	switch distribution {
	case DistributionUniform, DistributionHotspot, DistributionSkewed:
		return nil
	default:
		return fmt.Errorf("unsupported spatial distribution %q", distribution)
	}
}

func SamplePoint(random *rand.Rand, bounds geo.BoundingBox, distribution SpatialDistribution) geo.GeoPoint {
	latitudeSpan := bounds.MaxLatitude - bounds.MinLatitude
	longitudeSpan := bounds.MaxLongitude - bounds.MinLongitude

	var latitudeRatio, longitudeRatio float64
	switch distribution {
	case DistributionHotspot:
		// 85% 的点出现在热点附近
		if random.Float64() < 0.85 {
			center := hotspotCenters[random.Intn(len(hotspotCenters))]
			latitudeRatio = center.latitudeRatio + random.NormFloat64()*0.035
			longitudeRatio = center.longitudeRatio + random.NormFloat64()*0.035
		} else {
			latitudeRatio = random.Float64()
			longitudeRatio = random.Float64()
		}
	case DistributionSkewed:
		latitudeRatio = cube(random.Float64())
		longitudeRatio = cube(random.Float64())
	default:
		latitudeRatio = random.Float64()
		longitudeRatio = random.Float64()
	}

	latitudeRatio = clampUnit(latitudeRatio)
	longitudeRatio = clampUnit(longitudeRatio)
	return geo.GeoPoint{
		Latitude:  bounds.MinLatitude + latitudeRatio*latitudeSpan,
		Longitude: bounds.MinLongitude + longitudeRatio*longitudeSpan,
	}
}

func cube(value float64) float64 {
	return value * value * value
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// mix64 is the SplitMix64 finalizer. It provides stable domain-derived seeds
// and a one-to-one mapping for deterministic order IDs.
// 输入一个 uint64，经过充分混合后得到另一个看起来高度随机的 uint64
func Mix64(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}
