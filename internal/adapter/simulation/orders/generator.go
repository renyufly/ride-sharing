package orders

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"ride-sharing/internal/adapter/simulation"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/platform/config"
	"time"
)

// NewOrderStream returns a fresh replayable stream. Calling it again with the
// same arguments restarts the exact order sequence from Sequence 0.
// Streaming Generation（流式生成）
func NewStream(scenario simulation.Scenario, count int, window time.Duration, arrival config.ArrivalModel, distribution config.SpatialDistribution) (*OrderStream, error) {
	if count <= 0 {
		return nil, errors.New("order count must be greater than zero")
	}
	if window < 0 {
		return nil, errors.New("arrival window cannot be negative")
	}
	if err := config.ValidateArrivalModel(arrival); err != nil {
		return nil, err
	}
	if err := config.ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	return &OrderStream{
		total:        uint64(count),
		window:       window,
		arrival:      arrival,
		distribution: distribution,
		bounds:       scenario.Bounds(),
		projector:    scenario.Projector(),
		orderSeed:    scenario.Seeds().Order,
		random:       rand.New(rand.NewSource(scenario.Seeds().Order)),
	}, nil
}

// 不保存全部订单，只记住 要生成多少订单、当前生成到第几个、随机数生成器状态
type OrderStream struct {
	nextSequence uint64
	total        uint64
	window       time.Duration
	arrival      config.ArrivalModel
	distribution config.SpatialDistribution
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
//
//	↓
//
// 只负责：
// "订单应该什么时候来"
// CSP pipeline
//
//	↓
//
// 负责：
// "真的什么时候把订单送进去"
func (s *OrderStream) Next() (order.Order, bool, error) {
	// 返回值 bool 表示 还有没有订单

	if s.nextSequence >= s.total {
		return order.Order{}, false, nil
	}

	sequence := s.nextSequence

	// 生成订单位置
	location := simulation.SamplePoint(s.random, s.bounds, s.distribution)
	point, err := s.projector.Project(location)
	if err != nil {
		return order.Order{}, false, fmt.Errorf("project order sequence %d: %w", sequence, err)
	}

	order := order.Order{
		ID:               simulation.Mix64(uint64(s.orderSeed) + sequence), // Order ID 不是 Sequence
		Sequence:         sequence,
		PlannedArrivalNs: plannedArrival(sequence, s.total, s.window, s.arrival).Nanoseconds(),
		Pickup:           location,
		Point:            point,
	}
	s.nextSequence++
	return order, true, nil
}

// 制定订单的到达时间表
// 根据“当前是第几个订单”、订单总数、总到达时间窗口以及到达模型，计算这个订单应该在程序启动后的第多久出现
// sequence-当前订单编号 total-总订单数量 window-所有订单计划到达的总时间窗口
// 返回值-当前订单相对于开始时刻的计划到达时间
func plannedArrival(sequence, total uint64, window time.Duration, arrival config.ArrivalModel) time.Duration {
	if arrival == config.ArrivalUnbounded || total <= 1 || window == 0 {
		// unbounded：所有订单立即生产，计划时间为0
		return 0
	}

	switch arrival {
	case config.ArrivalUniformWindow:
		return uniformArrival(sequence, total, window)

	case config.ArrivalFrontLoadedBurst:
		return burstArrival(sequence, total, window)

	default:
		return 0
	}

}

func uniformArrival(sequence, total uint64, window time.Duration) time.Duration {
	// 时间均匀分配给每个订单
	return slotOffset(sequence, total, window)
}

func burstArrival(sequence, total uint64, window time.Duration) time.Duration {
	if total == 0 {
		return 0
	}

	// 80%
	burstOrderCount := uint64(math.Round(float64(total) * 0.8))

	// 防止上下溢出
	if burstOrderCount < 1 {
		burstOrderCount = 1
	} else if burstOrderCount > total {
		burstOrderCount = total
	}

	normalOrderCount := total - burstOrderCount // 20%

	burstWindow := window / 5 // 前20%时间
	normalWindow := window - burstWindow

	if sequence < uint64(burstOrderCount) {
		return slotOffset(sequence, burstOrderCount, burstWindow)
	}

	if normalOrderCount == 0 {
		return slotOffset(sequence, burstOrderCount, burstWindow)
	}

	// 剩余的都是后20%
	sequence -= burstOrderCount
	return burstWindow + slotOffset(sequence, normalOrderCount, normalWindow)
}

// 在当前时间window里依据指定的订单总数全部平均分配的时间戳
func slotOffset(index, count uint64, duration time.Duration) time.Duration {
	if count <= 1 {
		return 0
	}

	// ( 持续总时间 ÷ 总数 ) × index
	remainder := uint64(duration) % count
	offset := uint64(duration)/count*index + uint64(index*remainder/count)
	return time.Duration(offset)
}
