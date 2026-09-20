// Package generator creates deterministic riders and a replayable, streaming
// sequence of orders without retaining all orders in memory.
package generator

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"ride-sharing/internal/geo"
	"ride-sharing/internal/model"
)

// 压测数据发生器

type ArrivalModel string  // 订单什么时候到
// uniform-window 均匀到达：订单均匀铺在整个 30 秒窗口
// front-loaded-burst 前置突发流量：前 20% 时间到达 80% 的订单
// unbounded 所有订单都可以立即进入系统 (测系统纯粹的最大吞吐能力, 不模拟真实到达时间)
const (
	ArrivalUniformWindow    ArrivalModel = "uniform-window"
	ArrivalFrontLoadedBurst ArrivalModel = "front-loaded-burst"
	ArrivalUnbounded        ArrivalModel = "unbounded"
)

type SpatialDistribution string  // 骑手/订单在哪里
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

type Generator struct {
	bounds    geo.BoundingBox
	projector geo.Projector
	seeds     Seeds
}

func New(masterSeed int64, bounds geo.BoundingBox) (Generator, error) {
	if err := bounds.Validate(); err != nil {
		return Generator{}, fmt.Errorf("invalid generator bounds: %w", err)
	}
	projector, err := geo.NewProjector(bounds.Center())
	if err != nil {
		return Generator{}, err
	}

	return Generator{
		bounds:    bounds,
		projector: projector,
		seeds: Seeds{
			Rider: int64(mix64(uint64(masterSeed) ^ 0x7269646572736565)),
			Order: int64(mix64(uint64(masterSeed) ^ 0x6f72646572736565)),
		},
	}, nil
}

func (g Generator) Bounds() geo.BoundingBox {
	return g.bounds
}

func (g Generator) Origin() model.GeoPoint {
	return g.projector.Origin()
}

func (g Generator) Seeds() Seeds {
	return g.seeds
}

// 一次生成所有骑手
func (g Generator) GenerateRiders(count int, distribution SpatialDistribution) ([]model.Rider, error) {
	if count <= 0 {
		return nil, errors.New("rider count must be greater than zero")
	}
	if err := ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	random := rand.New(rand.NewSource(g.seeds.Rider))
	riders := make([]model.Rider, count)

	// 这里骑手数量相比千万订单小很多，
	// 而且后面的匹配算法本来就需要长期持有骑手数据，
	// 所以一次性放内存是合理的

	for index := range riders {
		location := samplePoint(random, g.bounds, distribution)
		point, err := g.projector.Project(location)
		if err != nil {
			return nil, fmt.Errorf("project rider %d: %w", index+1, err)
		}
		riders[index] = model.Rider{
			UID:      uint64(index + 1),
			Location: location,
			Point:    point,
		}
	}
	return riders, nil
}

// NewOrderStream returns a fresh replayable stream. Calling it again with the
// same arguments restarts the exact order sequence from Sequence 0.
// Streaming Generation（流式生成）
func (g Generator) NewOrderStream(count int, window time.Duration, arrival ArrivalModel, distribution SpatialDistribution) (*OrderStream, error) {
	if count <= 0 {
		return nil, errors.New("order count must be greater than zero")
	}
	if window < 0 {
		return nil, errors.New("arrival window cannot be negative")
	}
	if err := ValidateArrivalModel(arrival); err != nil {
		return nil, err
	}
	if err := ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	return &OrderStream{
		total:        uint64(count),
		window:       window,
		arrival:      arrival,
		distribution: distribution,
		bounds:       g.bounds,
		projector:    g.projector,
		orderSeed:    g.seeds.Order,
		random:       rand.New(rand.NewSource(g.seeds.Order)),
	}, nil
}

// 不保存全部订单，只记住 要生成多少订单、当前生成到第几个、随机数生成器状态
type OrderStream struct {
	nextSequence uint64
	total        uint64
	window       time.Duration
	arrival      ArrivalModel
	distribution SpatialDistribution
	bounds       geo.BoundingBox
	projector    geo.Projector
	orderSeed    int64
	random       *rand.Rand
}

// Next generates one order. It does not sleep until PlannedArrivalNs; the CSP
// pipeline added later owns pacing and admission-delay measurement.
// 整个订单生成器的核心
// stream.Next()调用后订单立即生成
// Generator
//     ↓
// 只负责：
// "订单应该什么时候来"
// CSP pipeline
//     ↓
// 负责：
// "真的什么时候把订单送进去"
func (s *OrderStream) Next() (model.Order, bool, error) {
	// 返回值 bool 表示 还有没有订单

	if s.nextSequence >= s.total {
		return model.Order{}, false, nil
	}

	sequence := s.nextSequence

	// 生成订单位置
	location := samplePoint(s.random, s.bounds, s.distribution)
	point, err := s.projector.Project(location)
	if err != nil {
		return model.Order{}, false, fmt.Errorf("project order sequence %d: %w", sequence, err)
	}

	order := model.Order{
		ID:               mix64(uint64(s.orderSeed) + sequence),  // Order ID 不是 Sequence
		Sequence:         sequence,
		PlannedArrivalNs: plannedArrival(sequence, s.total, s.window, s.arrival).Nanoseconds(),
		Pickup:           location,
		Point:            point,
	}
	s.nextSequence++
	return order, true, nil
}

func plannedArrival(sequence, total uint64, window time.Duration, arrival ArrivalModel) time.Duration {
	if arrival == ArrivalUnbounded || total <= 1 || window == 0 {
		return 0
	}
	if sequence == total-1 {
		return window
	}

	position := float64(sequence) / float64(total-1)
	if arrival == ArrivalFrontLoadedBurst {
		// Schedule 80% of orders in the first 20% of the window, then spread
		// the remaining 20% across the final 80%.
		if position <= 0.8 {
			position = position * 0.25
		} else {
			position = 0.2 + (position-0.8)*4
		}
	}
	return time.Duration(float64(window) * position)
}

func ValidateArrivalModel(arrival ArrivalModel) error {
	switch arrival {
	case ArrivalUniformWindow, ArrivalFrontLoadedBurst, ArrivalUnbounded:
		return nil
	default:
		return fmt.Errorf("unsupported arrival model %q", arrival)
	}
}

func ValidateSpatialDistribution(distribution SpatialDistribution) error {
	switch distribution {
	case DistributionUniform, DistributionHotspot, DistributionSkewed:
		return nil
	default:
		return fmt.Errorf("unsupported spatial distribution %q", distribution)
	}
}

func samplePoint(random *rand.Rand, bounds geo.BoundingBox, distribution SpatialDistribution) model.GeoPoint {
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
	return model.GeoPoint{
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
func mix64(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}
