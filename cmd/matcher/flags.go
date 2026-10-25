package main

import (
	"flag"
	"fmt"
	"io"
	"ride-sharing/internal/adapter/simulation"
	"ride-sharing/internal/adapter/simulation/orders"
	"ride-sharing/internal/platform/config"
)

// Parse reads command-line arguments without using the process-global FlagSet,
// which keeps configuration tests isolated and deterministic.
func parseConfig(args []string) (config.Config, error) {
	cfg := config.Default()
	flags := flag.NewFlagSet("matcher", flag.ContinueOnError) // 创建一个独立的命令行参数解析器 (为了测试隔离)
	flags.SetOutput(io.Discard)                               // flag 解析错误时，不要自动往终端打印东西

	algorithm := string(cfg.Algorithm)
	arrivalModel := string(cfg.ArrivalModel)
	riderDistribution := string(cfg.RiderDistribution)
	orderDistribution := string(cfg.OrderDistribution)
	strategy := string(cfg.Strategy)
	// 骑手数量
	flags.IntVar(&cfg.RiderCount, "riders", cfg.RiderCount, "number of riders")
	// 订单数量
	flags.IntVar(&cfg.OrderCount, "orders", cfg.OrderCount, "number of orders")
	// 订单到达窗口 (Y 个订单计划到达系统的时间范围)
	flags.DurationVar(&cfg.ArrivalWindow, "arrival-window", cfg.ArrivalWindow, "order arrival window")
	flags.DurationVar(&cfg.RunTimeout, "timeout", cfg.RunTimeout, "whole-run timeout; zero disables the deadline")
	flags.DurationVar(&cfg.MonitorInterval, "monitor-interval", cfg.MonitorInterval, "Go runtime resource sampling interval")
	// 订单生成模型
	// 注意: unbounded-所有订单立即到达；ArrivalWindow 只作为统计观察窗口
	flags.StringVar(&arrivalModel, "arrival-model", arrivalModel, "arrival model: uniform-window, front-loaded-burst, or unbounded")
	flags.Int64Var(&cfg.Seed, "seed", cfg.Seed, "deterministic random seed")
	flags.StringVar(&riderDistribution, "rider-distribution", riderDistribution, "rider distribution: uniform, hotspot, or skewed")
	flags.StringVar(&orderDistribution, "order-distribution", orderDistribution, "order distribution: uniform, hotspot, or skewed")
	flags.StringVar(&algorithm, "algorithm", algorithm, "matching algorithm: brute-force or kd-tree")
	flags.IntVar(&cfg.Workers, "workers", cfg.Workers, "matching worker count")
	flags.IntVar(&cfg.BatchSize, "batch-size", cfg.BatchSize, "orders per batch")
	flags.IntVar(&cfg.ChannelCapacity, "channel-capacity", cfg.ChannelCapacity, "bounded batch channel capacity")
	flags.StringVar(&strategy, "strategy", strategy, "matching strategy: nearest or balanced")
	flags.IntVar(&cfg.TopK, "top-k", cfg.TopK, "strategy B nearest-candidate count")
	flags.Float64Var(&cfg.MaxExtraDistanceMeters, "max-extra-distance", cfg.MaxExtraDistanceMeters, "strategy B maximum distance beyond the nearest rider in meters")

	flags.IntVar(&cfg.MaxOrdersPerRider, "max-orders-per-rider", cfg.MaxOrdersPerRider, "Max Orders Per Rider")

	flags.StringVar(&cfg.DeferredOutput, "deferred-output", cfg.DeferredOutput, "output file of deferred orders")
	flags.UintVar(&cfg.Attempt, "attempt", cfg.Attempt, "current round of attemptation")

	flags.StringVar(&cfg.DeferredInput, "deferred-input", cfg.DeferredInput, "input file of deferred orders")

	if err := flags.Parse(args); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	cfg.Algorithm = config.Algorithm(algorithm)
	cfg.ArrivalModel = orders.ArrivalModel(arrivalModel)
	cfg.RiderDistribution = simulation.SpatialDistribution(riderDistribution)
	cfg.OrderDistribution = simulation.SpatialDistribution(orderDistribution)
	cfg.Strategy = config.Strategy(strategy)
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
