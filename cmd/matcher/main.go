// Command matcher is the standalone entry point for the matching exercise.
//
// It exposes the frozen nearest baseline and the explicitly selected balanced
// extension as separate bounded CSP pipelines.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"time"

	"ride-sharing/internal/config"
	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/matcher/bruteforce"
	"ride-sharing/internal/matcher/kdtree"
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/report"
	"ride-sharing/internal/resource"
)

// 整个“骑手-订单匹配程序”的总执行
// 负责把 config → generator → matcher → pipeline → report/resource
// 这些模块串起来，最后输出一份 JSON 实验报告

// main.go 负责组装系统，真正的并发匹配逻辑在 pipeline.Run() / pipeline.RunBalanced() 里面
type startupOutput struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Config  config.DisplayConfig `json:"config"`
	Data    dataOutput           `json:"data"`
}

type dataOutput struct {
	Bounds               boundsOutput                     `json:"bounds"`
	ProjectionOrigin     geoPointOutput                   `json:"projectionOrigin"`
	EarthRadiusMeters    float64                          `json:"earthRadiusMeters"`
	RiderSeed            int64                            `json:"riderSeed"`
	OrderSeed            int64                            `json:"orderSeed"`
	GeneratedRiderCount  int                              `json:"generatedRiderCount"`
	GeneratedOrderCount  int                              `json:"generatedOrderCount"`
	AdmittedOrderCount   int                              `json:"admittedOrderCount"`
	MatchedOrderCount    int                              `json:"matchedOrderCount"`
	UnfinishedOrderCount int                              `json:"unfinishedOrderCount"`
	LastPlannedArrivalNs int64                            `json:"lastPlannedArrivalNs"`
	MaxQueueDepth        int                              `json:"maxQueueDepthBatches"`
	WorkerCompleted      []uint64                         `json:"workerCompletedOrders"`
	GOMAXPROCS           int                              `json:"gomaxprocs"`
	MemoryEstimate       *pipeline.MemoryEstimate         `json:"memoryEstimate,omitempty"`
	BalancedMemory       *pipeline.BalancedMemoryEstimate `json:"balancedMemoryEstimate,omitempty"`
	Index                matchrule.IndexStats             `json:"index"`
	Timing               timingOutput                     `json:"timing"`
	Performance          pipeline.PerformanceMetrics      `json:"performance"`
	Resources            resource.Stats                   `json:"resources"`
	RiderPreview         []riderOutput                    `json:"riderPreview"`
	OrderPreview         []orderOutput                    `json:"orderPreview"`
	AssignmentPreview    []assignmentOutput               `json:"assignmentPreview"`
	Report               report.Summary                   `json:"report"`
	StrategyDetails      *pipeline.StrategyMetrics        `json:"strategyDetails,omitempty"`
}

type boundsOutput struct {
	MinLatitude  float64 `json:"minLatitude"`
	MaxLatitude  float64 `json:"maxLatitude"`
	MinLongitude float64 `json:"minLongitude"`
	MaxLongitude float64 `json:"maxLongitude"`
}

type geoPointOutput struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type pointOutput struct {
	X float64 `json:"xMeters"`
	Y float64 `json:"yMeters"`
}

type riderOutput struct {
	UID      uint64         `json:"uid"`
	Location geoPointOutput `json:"location"`
	Point    pointOutput    `json:"point"`
}

type orderOutput struct {
	ID               uint64         `json:"id"`
	Sequence         uint64         `json:"sequence"`
	PlannedArrivalNs int64          `json:"plannedArrivalNs"`
	Pickup           geoPointOutput `json:"pickup"`
	Point            pointOutput    `json:"point"`
}

type assignmentOutput struct {
	OrderID               uint64  `json:"orderId"`
	Sequence              uint64  `json:"sequence"`
	RiderUID              uint64  `json:"riderUid"`
	DistanceSquaredMeters float64 `json:"distanceSquaredMeters"`
}

// 面向接口编程
type nearestMatcher interface {  
	Match(model.Order) (model.Assignment, error)
	TopKInto(model.Order, int, []matchrule.Candidate) ([]matchrule.Candidate, error)
	IndexStats() matchrule.IndexStats
}

type timingOutput struct {
	RiderGenerationNs int64   `json:"riderGenerationNs"`
	IndexBuildNs      int64   `json:"indexBuildNs"`
	PipelineNs        int64   `json:"pipelineNs"`
	TotalNs           int64   `json:"totalNs"`
	ThroughputPerSec  float64 `json:"throughputOrdersPerSecond"`
}

func main() {
	cfg, err := config.Parse(os.Args[1:]) // 读取配置
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid matcher configuration: %v\n", err)
		os.Exit(2)
	}
	runtime.GC()  // 主动触发一次 Go GC, 为了性能测试更加干净
	totalStarted := time.Now()
	resourceMonitor, err := resource.Start(cfg.MonitorInterval) // 启动资源监控, 定期记录：CPU 内存 GC goroutine
	if err != nil {
		fmt.Fprintf(os.Stderr, "start resource monitor: %v\n", err)
		os.Exit(1)
	}

	riderGenerationStarted := time.Now()
	dataGenerator, err := generator.New(cfg.Seed, geo.SanFranciscoBounds) // 创建随机数据生成器，生成骑手
	if err != nil {
		fmt.Fprintf(os.Stderr, "create data generator: %v\n", err)
		os.Exit(1)
	}
	riders, err := dataGenerator.GenerateRiders(cfg.RiderCount, cfg.RiderDistribution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate riders: %v\n", err)
		os.Exit(1)
	}
	riderGenerationDuration := time.Since(riderGenerationStarted) // 统计生成时间
	
	indexBuildStarted := time.Now() // KD-Tree 正式匹配之前需要建索引
	
	var selectedMatcher nearestMatcher
	// 创建 Matcher：两种算法“找附近骑手”
	switch cfg.Algorithm {
	case config.AlgorithmBruteForce:
		selectedMatcher, err = bruteforce.New(riders)
	case config.AlgorithmKDTree:
		selectedMatcher, err = kdtree.New(riders)
	default:
		err = fmt.Errorf("unsupported algorithm %q", cfg.Algorithm)
	}
	
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s matcher: %v\n", cfg.Algorithm, err)
		os.Exit(1)
	}

	// indexBuildDuration - 建立 KDTree 花多久
	indexBuildDuration := time.Since(indexBuildStarted)
	
	// 创建订单流，而不是直接创建 1 万订单
	stream, err := dataGenerator.NewOrderStream(cfg.OrderCount, cfg.ArrivalWindow, cfg.ArrivalModel, cfg.OrderDistribution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create order stream: %v\n", err)
		os.Exit(1)
	}

	const previewSize = 3  // 只 Preview 3 条数据(完整数据参与计算，只保存极少数样本用于验证)

	// Context + Goroutine 生命周期管理：控制整个并发 Pipeline 生命周期
	runContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	// 支持超时 (pipeline 最多运行 x 秒，之后所有 goroutine停止)
	if cfg.RunTimeout > 0 {
		var cancelTimeout context.CancelFunc
		runContext, cancelTimeout = context.WithTimeout(runContext, cfg.RunTimeout)
		defer cancelTimeout()
	}

	// CSP Pipeline 核心调优参数
	pipelineOptions := pipeline.Options{
		Workers:         cfg.Workers,
		BatchSize:       cfg.BatchSize,   // 一次处理多少订单
		ChannelCapacity: cfg.ChannelCapacity,
		ExpectedOrders:  uint64(cfg.OrderCount),
		PreviewSize:     previewSize,
	}

	// 提前估算内存
	// 不要因为订单总数是 1000 万，就让内存同时保存 1000 万个订单
	// bounded CSP pipeline 中的 bounded
	var memoryEstimate *pipeline.MemoryEstimate
	if cfg.Strategy == config.StrategyNearest {
		estimate, estimateErr := pipeline.EstimateMemory(cfg.RiderCount, pipelineOptions)
		if estimateErr != nil {
			fmt.Fprintf(os.Stderr, "estimate pipeline memory: %v\n", estimateErr)
			os.Exit(1)
		}
		memoryEstimate = &estimate
	}

	var balancedMemory *pipeline.BalancedMemoryEstimate
	pipelineStarted := time.Now()

	// pipelineResult - 最终实验结果的核心
	var pipelineResult pipeline.Result
	
	// 真正开始并发匹配：pipeline.Run()
	switch cfg.Strategy {
	case config.StrategyNearest:
		// 距离优先
		pipelineResult, err = pipeline.Run(runContext, stream, selectedMatcher, riders, pipelineOptions)
	
	case config.StrategyBalanced:
		// 负载均衡-pipeline.RunBalanced()
		balancedOptions := pipeline.BalancedOptions{
			Options:                pipelineOptions,
			TopK:                   cfg.TopK,
			MaxExtraDistanceMeters: cfg.MaxExtraDistanceMeters,
		}
		estimate, estimateErr := pipeline.EstimateBalancedMemory(cfg.RiderCount, balancedOptions)
		if estimateErr != nil {
			fmt.Fprintf(os.Stderr, "estimate balanced pipeline memory: %v\n", estimateErr)
			os.Exit(1)
		}
		balancedMemory = &estimate
		pipelineResult, err = pipeline.RunBalanced(runContext, stream, selectedMatcher, riders, balancedOptions)
	default:
		err = fmt.Errorf("unsupported strategy %q", cfg.Strategy)
	}
	
	// 计算性能

	// pipelineDuration-真正匹配订单花多久
	pipelineDuration := time.Since(pipelineStarted)
	
	resourceStats := resourceMonitor.Stop()  // 停止资源监控
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// totalDuration - 整个程序总耗时
	totalDuration := time.Since(totalStarted)
	
	// throughput：x订单/秒，系统每秒可以完成多少笔订单匹配
	throughput := 0.0 
	if pipelineDuration > 0 {
		throughput = float64(pipelineResult.CompletedOrders) / pipelineDuration.Seconds()
	}

	bounds := dataGenerator.Bounds()
	seeds := dataGenerator.Seeds()
	phase := fmt.Sprintf("csp-%s-report", cfg.Algorithm)
	message := "all orders matched exactly through the bounded CSP pipeline"
	if cfg.Strategy == config.StrategyBalanced {
		phase = fmt.Sprintf("csp-balanced-%s-report", cfg.Algorithm)
		message = "all orders assigned deterministically through the load-aware bounded CSP pipeline"
	}

	// 把之前计算出来的结果装进一个 JSON DTO
	output := startupOutput{
		Phase:   phase,
		Message: message,
		Config:  cfg.Display(),
		Data: dataOutput{
			Bounds: boundsOutput{
				MinLatitude:  bounds.MinLatitude,
				MaxLatitude:  bounds.MaxLatitude,
				MinLongitude: bounds.MinLongitude,
				MaxLongitude: bounds.MaxLongitude,
			},
			ProjectionOrigin:     displayGeoPoint(dataGenerator.Origin()),
			EarthRadiusMeters:    geo.EarthRadiusMeters,
			RiderSeed:            seeds.Rider,
			OrderSeed:            seeds.Order,
			GeneratedRiderCount:  len(riders),
			GeneratedOrderCount:  int(pipelineResult.GeneratedOrders),
			AdmittedOrderCount:   int(pipelineResult.AdmittedOrders),
			MatchedOrderCount:    int(pipelineResult.CompletedOrders),
			UnfinishedOrderCount: int(pipelineResult.UnfinishedOrders),
			LastPlannedArrivalNs: pipelineResult.LastPlannedArrivalNs,
			MaxQueueDepth:        pipelineResult.MaxQueueDepth,
			WorkerCompleted:      pipelineResult.WorkerCompleted,
			GOMAXPROCS:           runtime.GOMAXPROCS(0),
			MemoryEstimate:       memoryEstimate,
			BalancedMemory:       balancedMemory,
			Index:                selectedMatcher.IndexStats(),
			Timing: timingOutput{
				RiderGenerationNs: riderGenerationDuration.Nanoseconds(),
				IndexBuildNs:      indexBuildDuration.Nanoseconds(),
				PipelineNs:        pipelineDuration.Nanoseconds(),
				TotalNs:           totalDuration.Nanoseconds(),
				ThroughputPerSec:  throughput,
			},
			Performance:       pipelineResult.Performance,
			Resources:         resourceStats,
			RiderPreview:      displayRiders(riders, previewSize),
			OrderPreview:      displayOrders(pipelineResult.OrderPreview),
			AssignmentPreview: displayAssignments(pipelineResult.AssignmentPreview),
			Report:            pipelineResult.Report,
			StrategyDetails:   pipelineResult.StrategyDetails,
		},
	}

	// json.NewEncoder 最终输出
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "write startup output: %v\n", err)
		os.Exit(1)
	}
}

// displayXXX：DTO 转换: 内部业务 Model -> 用于 JSON 展示的 Model
// 内部数据结构和对外输出格式解耦
func displayRiders(riders []model.Rider, limit int) []riderOutput {
	if limit > len(riders) {
		limit = len(riders)
	}
	preview := make([]riderOutput, limit)
	for index, rider := range riders[:limit] {
		preview[index] = riderOutput{
			UID:      rider.UID,
			Location: displayGeoPoint(rider.Location),
			Point:    displayPoint(rider.Point),
		}
	}
	return preview
}

func displayOrder(order model.Order) orderOutput {
	return orderOutput{
		ID:               order.ID,
		Sequence:         order.Sequence,
		PlannedArrivalNs: order.PlannedArrivalNs,
		Pickup:           displayGeoPoint(order.Pickup),
		Point:            displayPoint(order.Point),
	}
}

func displayOrders(orders []model.Order) []orderOutput {
	result := make([]orderOutput, len(orders))
	for index, order := range orders {
		result[index] = displayOrder(order)
	}
	return result
}

func displayAssignment(assignment model.Assignment) assignmentOutput {
	return assignmentOutput{
		OrderID:               assignment.OrderID,
		Sequence:              assignment.Sequence,
		RiderUID:              assignment.RiderUID,
		DistanceSquaredMeters: assignment.DistanceSquaredMeters,
	}
}

func displayAssignments(assignments []model.Assignment) []assignmentOutput {
	result := make([]assignmentOutput, len(assignments))
	for index, assignment := range assignments {
		result[index] = displayAssignment(assignment)
	}
	return result
}

func displayGeoPoint(point model.GeoPoint) geoPointOutput {
	return geoPointOutput{Latitude: point.Latitude, Longitude: point.Longitude}
}

func displayPoint(point model.Point2D) pointOutput {
	return pointOutput{X: point.X, Y: point.Y}
}
