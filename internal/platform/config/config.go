// Package config owns the standalone matcher's runtime configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"ride-sharing/internal/adapter/simulation/orders"
	"time"

	"ride-sharing/internal/adapter/simulation"
)

// errors      创建/合并错误
// flag        解析命令行参数
// fmt         格式化错误信息
// io          这里用于丢弃 flag 默认输出
// math        检查 NaN、Inf
// time        时间类型
// generator   使用订单到达模型、空间分布模型

// 整个骑手匹配程序的“控制面板 + 参数检查器”
// 这次测试跑多少骑手、多少订单、订单多久到达、用什么算法、
// 开几个 goroutine、channel 多大、采用什么匹配策略？
type Algorithm string

// 程序支持两种算法：
// brute-force 暴力搜索
// kd-tree 最近邻搜索  (通过空间索引快速找附近骑手)
const (
	AlgorithmBruteForce Algorithm = "brute-force"
	AlgorithmKDTree     Algorithm = "kd-tree"
)

// Algorithm = “怎么快速找候选Rider？”
// Strategy = “找到候选骑手以后，怎么决定给谁？”

type Strategy string

// nearest 直接给最近骑手
// balanced 在附近若干骑手中考虑订单量，尽量均衡
const (
	StrategyNearest  Strategy = "nearest"
	StrategyBalanced Strategy = "balanced"
)

// Config contains the complete runtime surface. TopK and
// MaxExtraDistanceMeters are explicit strategy B controls; validation rejects
// them for the default nearest-rider strategy.
type Config struct { // 一次 benchmark 的完整配置
	RiderCount             int                            // 骑手/司机 数量
	OrderCount             int                            // 订单数量
	ArrivalWindow          time.Duration                  // 订单在多久内到达
	RunTimeout             time.Duration                  // 整个测试最大运行时间
	MonitorInterval        time.Duration                  // 多久采样一次资源使用
	ArrivalModel           orders.ArrivalModel            // uniform-window-订单均匀进入，front-loaded-burst-大量订单集中在开始阶段，unbounded-不按照固定 arrival window 节流
	Seed                   int64                          // 随机数种子
	RiderDistribution      simulation.SpatialDistribution // 骑手怎么分布：uniform-均匀分布，hotspot-大量订单集中在热点区域
	OrderDistribution      simulation.SpatialDistribution
	Algorithm              Algorithm // brute-force / KD-tree
	Workers                int       // worker goroutine 数量
	BatchSize              int       // 每批多少订单
	ChannelCapacity        int       // channel 缓冲大小
	Strategy               Strategy
	TopK                   int     // balanced 考虑最近几个骑手
	MaxExtraDistanceMeters float64 // 为均衡最多允许多远 (防止 balanced 做得太过头)
	MaxOrdersPerRider      int     // 限制每个骑手最多订单上限
	DeferredOutput         string  // deferred 输出的jsonL文件
	Attempt                uint    // 第几轮尝试
	DeferredInput          string  // deferred 输入的jsonL文件
}

// DisplayConfig is the stable, human-readable representation printed at
// startup. Durations are strings so a run can be copied into an interview or
// benchmark report without converting nanoseconds.
type DisplayConfig struct { // 方便输出报告
	RiderCount             int     `json:"riderCount"` // struct tag：告诉 JSON 编码器输出的名字
	OrderCount             int     `json:"orderCount"`
	ArrivalWindow          string  `json:"arrivalWindow"`
	RunTimeout             string  `json:"runTimeout"`
	MonitorInterval        string  `json:"monitorInterval"`
	ArrivalModel           string  `json:"arrivalModel"`
	Seed                   int64   `json:"seed"`
	RiderDistribution      string  `json:"riderDistribution"`
	OrderDistribution      string  `json:"orderDistribution"`
	Algorithm              string  `json:"algorithm"`
	Workers                int     `json:"workers"`
	BatchSize              int     `json:"batchSize"`
	ChannelCapacity        int     `json:"channelCapacity"`
	Strategy               string  `json:"strategy"`
	TopK                   int     `json:"topK"`
	MaxExtraDistanceMeters float64 `json:"maxExtraDistanceMeters"`
	MaxOrdersPerRider      int     `json:"maxOrdersPerRider"`
	DeferredOutput         string  `json:"deferredOutput"` // deferred 输出的jsonL文件
	Attempt                uint    `json:"attempt"`
	DeferredInput          string  `json:"deferredInput"`
}

// 默认配置：
// 100 骑手
// 10000 订单
// 30 秒到达窗口
// KD-tree
// 2 workers
// nearest strategy
func Default() Config {
	return Config{
		RiderCount:        100,
		OrderCount:        10_000,
		ArrivalWindow:     30 * time.Second,
		RunTimeout:        0,
		MonitorInterval:   100 * time.Millisecond,
		ArrivalModel:      orders.ArrivalUniformWindow,
		Seed:              42,
		RiderDistribution: simulation.DistributionUniform,
		OrderDistribution: simulation.DistributionUniform,
		Algorithm:         AlgorithmKDTree,
		Workers:           2,
		BatchSize:         1,
		ChannelCapacity:   16,
		Strategy:          StrategyNearest,
		Attempt:           1,
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.RiderCount <= 0 {
		errs = append(errs, errors.New("riders must be greater than zero"))
	}
	// 订单数校验
	if c.OrderCount <= 0 {
		errs = append(errs, errors.New("orders must be greater than zero"))
	}
	if c.ArrivalWindow <= 0 {
		errs = append(errs, errors.New("arrival window cannot be negative or zero"))
	}
	if c.RunTimeout < 0 {
		errs = append(errs, errors.New("run timeout cannot be negative"))
	}
	if c.MonitorInterval <= 0 {
		errs = append(errs, errors.New("monitor interval must be greater than zero"))
	}
	if err := orders.ValidateArrivalModel(c.ArrivalModel); err != nil {
		errs = append(errs, err)
	}
	if err := simulation.ValidateSpatialDistribution(c.RiderDistribution); err != nil {
		errs = append(errs, fmt.Errorf("rider distribution: %w", err))
	}
	if err := simulation.ValidateSpatialDistribution(c.OrderDistribution); err != nil {
		errs = append(errs, fmt.Errorf("order distribution: %w", err))
	}
	if c.Algorithm != AlgorithmBruteForce && c.Algorithm != AlgorithmKDTree {
		errs = append(errs, fmt.Errorf("unsupported algorithm %q", c.Algorithm))
	}
	if c.Workers <= 0 {
		errs = append(errs, errors.New("workers must be greater than zero"))
	}
	if c.BatchSize <= 0 {
		errs = append(errs, errors.New("batch size must be greater than zero"))
	}
	if c.ChannelCapacity <= 0 {
		errs = append(errs, errors.New("channel capacity must be greater than zero"))
	}
	switch c.Strategy {
	case StrategyNearest:
		if c.TopK != 0 || c.MaxExtraDistanceMeters != 0 {
			errs = append(errs, errors.New("strategy B options require --strategy=balanced"))
		}
	case StrategyBalanced:
		if c.TopK <= 0 {
			errs = append(errs, errors.New("top-k must be greater than zero for balanced strategy"))
		}
		if c.TopK > c.RiderCount {
			errs = append(errs, errors.New("top-k cannot exceed rider count"))
		}
		if c.MaxExtraDistanceMeters < 0 || math.IsNaN(c.MaxExtraDistanceMeters) || math.IsInf(c.MaxExtraDistanceMeters, 0) {
			errs = append(errs, errors.New("max extra distance must be finite and non-negative"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported strategy %q", c.Strategy))
	}
	return errors.Join(errs...)
}

func (c Config) Display() DisplayConfig { //  把内部运行配置转换成人类方便阅读的版本
	return DisplayConfig{
		RiderCount:             c.RiderCount,
		OrderCount:             c.OrderCount,
		ArrivalWindow:          c.ArrivalWindow.String(),
		RunTimeout:             c.RunTimeout.String(),
		MonitorInterval:        c.MonitorInterval.String(),
		ArrivalModel:           string(c.ArrivalModel),
		Seed:                   c.Seed,
		RiderDistribution:      string(c.RiderDistribution),
		OrderDistribution:      string(c.OrderDistribution),
		Algorithm:              string(c.Algorithm),
		Workers:                c.Workers,
		BatchSize:              c.BatchSize,
		ChannelCapacity:        c.ChannelCapacity,
		Strategy:               string(c.Strategy),
		TopK:                   c.TopK,
		MaxExtraDistanceMeters: c.MaxExtraDistanceMeters,
		MaxOrdersPerRider:      c.MaxOrdersPerRider,
		DeferredOutput:         c.DeferredOutput, // deferred 输出的jsonL文件
		Attempt:                c.Attempt,
		DeferredInput:          c.DeferredInput,
	}
}
