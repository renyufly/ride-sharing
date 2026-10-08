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
	"time"

	"errors"

	"ride-sharing/internal/config"
	"ride-sharing/internal/deferred"
	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/report"
	"ride-sharing/internal/resource"

	"ride-sharing/internal/matchrun"
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
	GeneratedWithinWindow int							  `json:"generatedWithinWindow"`
	AdmittedOrderCount   int                              `json:"admittedOrderCount"`
	AdmittedWithinWindow int 							  `json:"admittedWithinWindow"`
	CompletedOrderCount  int                              `json:"completedOrderCount"`
	CompletedWithinWindow int 							  `json:"completedWithinWindow"`
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
	ReadableSummary		 readableSummary				  `json:"readableSummary"`
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
// type nearestMatcher interface {  
// 	Match(model.Order) (model.Assignment, error)
// 	TopKInto(model.Order, int, []matchrule.Candidate) ([]matchrule.Candidate, error)
// 	IndexStats() matchrule.IndexStats
// }

type timingOutput struct {
	RiderGenerationNs int64   `json:"riderGenerationNs"`
	IndexBuildNs      int64   `json:"indexBuildNs"`
	PipelineNs        int64   `json:"pipelineNs"`
	TotalNs           int64   `json:"totalNs"`
	ThroughputPerSec  float64 `json:"throughputOrdersPerSecond"`
}

type readableSummary struct {
	Workload    string `json:"workload"`
    Result      string `json:"result"`
    Strategy    string	`json:"strategy"`
    Timing      string	`json:"timing"`
    Load        string	`json:"load"`
    Distance    string	`json:"distance"`
	Algorithm   string  `json:"algorithm"`
	Resources   string  `json:"resources"`
}

func main() {
	cfg, err := config.Parse(os.Args[1:]) // 读取配置
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid matcher configuration: %v\n", err)
		os.Exit(2)
	}
	

	var deferredOrderSink pipeline.DeferredOrderSink
	var fileSink *deferred.FileSink

	switch cfg.Strategy {
	case config.StrategyBalanced:
		fileSink, err = deferred.NewFileSink(cfg.DeferredOutput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create deferred output sink: %v\n", err)
			os.Exit(1)
		}

		deferredOrderSink = fileSink
	case config.StrategyNearest:
		fileSink = nil
		deferredOrderSink = nil
	}


	// Context + Goroutine 生命周期管理：控制整个并发 Pipeline 生命周期
	runContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	// 支持超时 (pipeline 最多运行 x 秒，之后所有 goroutine停止)

	previewSize := 3

	request := matchrun.Request{
		Config: cfg,
		PreviewSize: previewSize,
		DeferredSink: deferredOrderSink,
	}

	runResult, runErr := matchrun.Run(runContext, request)

	if fileSink != nil {
		closeErr := fileSink.Close()
		if closeErr != nil {
			runErr = errors.Join(runErr, closeErr)
		}
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", runErr)
		os.Exit(1)
	}

	cfg = runResult.Config

	pipelineResult := runResult.Pipeline
	bounds := runResult.Bounds
	seeds := runResult.Seeds
	memoryEstimate := runResult.MemoryEstimate
	balancedMemory := runResult.BalancedMemory
	resourceStats := runResult.Resources
	riderGenerationDuration := runResult.Timing.RiderGeneration
	indexBuildDuration	:= runResult.Timing.IndexBuild
	pipelineDuration := runResult.Timing.Pipeline
	totalDuration :=runResult.Timing.Total
	throughput	:= runResult.Timing.ThroughputPerSecond
	

	phase := fmt.Sprintf("csp-%s-report", cfg.Algorithm)
	message := "all orders matched exactly through the bounded CSP pipeline"
	if cfg.Strategy == config.StrategyBalanced {
		phase = fmt.Sprintf("csp-balanced-%s-report", cfg.Algorithm)
		message = "all orders assigned deterministically through the load-aware bounded CSP pipeline"
	}

	// 时间值转换
	lastPlannedArrivalText := time.Duration(pipelineResult.LastPlannedArrivalNs).String()
	pipelineDurationText := pipelineDuration.String()
	endToEndP99Text := time.Duration(pipelineResult.Performance.EndToEndLatency.P99Ns).String()
	drainText := time.Duration(pipelineResult.Performance.DrainAfterWindowNs).String()

	// 拼接输出的字符串
	workloadText := fmt.Sprintf(
		"对于%d 名骑手, 总共生成%d 个订单，且订单按 %s 生成订单模型, 需在 %v 的时间窗口内生成",
		cfg.RiderCount,
		cfg.OrderCount,
		cfg.ArrivalModel,
		cfg.ArrivalWindow,
	)

	resultText := fmt.Sprintf(
		"在ArrivalWindow= %s 内, 生成订单: %d/%d，接收: %d/%d，被成功分配给骑手完成的订单数: %d/%d；时间窗口期结束最终未完成: %d",
		cfg.ArrivalWindow.String(),
		pipelineResult.GeneratedWithinWindow,
		pipelineResult.GeneratedOrders,
		pipelineResult.AdmittedWithinWindow,
		pipelineResult.AdmittedOrders,
		pipelineResult.CompletedWithinWindow,
		pipelineResult.CompletedOrders,
		pipelineResult.UnfinishedOrders,
	)

	// Algorithm = “怎么快速找候选Rider？”
	// Strategy = “找到候选骑手以后，怎么决定给谁？”
	strategyText := fmt.Sprintf(
		"默认策略A-最近骑手分配策略, 使用 %s 算法寻找候选骑手",
		cfg.Algorithm,
	)

	algorithmText := fmt.Sprintf(
		"策略A-最近邻搜索分配骑手算法用时 P50/P95/P99/Max=%s/%s/%s/%s",
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.NearestSearch.P50Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.NearestSearch.P95Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.NearestSearch.P99Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.NearestSearch.MaxNs).String(),
	)

	if cfg.Strategy == config.StrategyBalanced {
		strategyText = fmt.Sprintf(
		"策略B-负载均衡骑手分配, 使用 %s 算法寻找候选骑手，Top-K=%d，最大额外距离=%.2fm",
		cfg.Algorithm,
		cfg.TopK,
		cfg.MaxExtraDistanceMeters,
	)

		algorithmText = fmt.Sprintf(
		"策略B-负载均衡分配骑手, Top-K搜索骑手(使用KD-Tree) P50/P95/P99/Max=%s/%s/%s/%s, 最终决策选出最终骑手 P50/P95/P99/Max=%s/%s/%s/%s, 纯算法总耗时 P50/P95/P99/Max=%s/%s/%s/%s",
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.CandidateSearch.P50Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.CandidateSearch.P95Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.CandidateSearch.P99Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.CandidateSearch.MaxNs).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AssignmentDecision.P50Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AssignmentDecision.P95Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AssignmentDecision.P99Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AssignmentDecision.MaxNs).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute.P50Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute.P95Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute.P99Ns).String(),
		time.Duration(pipelineResult.Performance.AlgorithmPerformanceMetrics.AlgorithmCompute.MaxNs).String(),
	)
	}

	// Pipeline包含时间：
	// Producer 生成订单。
	// 按 Arrival 模型等待。
	// Batch/Channel 传输。
	// Candidate Worker 搜索 Top-K。
	// Coordinator 重排和选择骑手。
	// 报告统计。
	// Worker 结束和结果归并
	timingText := fmt.Sprintf(
		"最后的一个订单的生成时间=%s，整个Pipeline时间=%s (非纯匹配算法耗时)，端到端P99=%s (99%%的订单，从计划生成开始，到完成匹配所花的时间不超过) \n" +
		"最后一笔订单生成后系统还花了多久才完成全部订单=%s, 最后一个 Batch 在 Pipeline 启动后的什么时间成功写入匹配 Channel=%s \n" + 
		"整个系统截止到最后一单完成总共时间=%s, 主程序全部总时间=%s", 
		lastPlannedArrivalText,
		pipelineDurationText,
		endToEndP99Text,
		drainText,
		time.Duration(pipelineResult.Performance.ActualInjectionNs).String(),
		time.Duration(pipelineResult.Performance.TotalRunNs).String(),
		time.Duration(totalDuration.Nanoseconds()).String(),
	)
	
	loadText := fmt.Sprintf(
		"骑手额度=%d (0表示不限额), 主窗口内骑手被分配订单数量 min/mean/max=%d/%.2f/%d，零订单的骑手数量=%d，Coefficient Of Variation=%.4f" +
		"因骑手满额度而被defer的订单数=%d, 超时未分配而被defer的订单数=%d",
		pipelineResult.MaxOrdersPerRider,
		pipelineResult.Report.MinOrders,
		pipelineResult.Report.MeanOrders,
		pipelineResult.Report.MaxOrders,
		pipelineResult.Report.ZeroRiderCount,
		pipelineResult.Report.CoefficientOfVariation,
		pipelineResult.DeferredByCapacity,
		pipelineResult.DeferredByWindow,

	)

	distanceText := fmt.Sprintf(
		"平均匹配距离=%.2fm，P95=%.2fm，最大=%.2fm",
		pipelineResult.Report.AverageDistanceMeters,
		pipelineResult.Report.P95DistanceMeters,
		pipelineResult.Report.MaxDistanceMeters,
	)

	// Resource
	monitorTime := time.Duration(resourceStats.ElapsedNs)
	gcPauseTime:= time.Duration(resourceStats.GCPauseDeltaNs)

	resourceText := fmt.Sprintf(
		"资源监控时长=%s，采样=%d次（间隔%s）；"+
		"Go堆峰值=%dKB，HeapInuse峰值=%dKB，"+
		"Runtime Sys峰值=%dKB；"+
		"累计分配=%dKB，GC=%d次，GC暂停=%s，"+
		"Goroutine峰值=%d，GOMAXPROCS=%d",
		monitorTime, resourceStats.Samples, resourceStats.SampleInterval,
		resourceStats.PeakHeapAllocBytes/1024,
		resourceStats.PeakHeapInuseBytes/1024,
		resourceStats.PeakRuntimeSysBytes/1024,
		resourceStats.TotalAllocDeltaBytes/1024,
		resourceStats.NumGCDelta, gcPauseTime,
		resourceStats.PeakGoroutines, runResult.GOMAXPROCS,
	)


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
			ProjectionOrigin:     displayGeoPoint(runResult.ProjectionOrigin),
			EarthRadiusMeters:    runResult.EarthRadiusMeter,
			RiderSeed:            seeds.Rider,
			OrderSeed:            seeds.Order,
			GeneratedRiderCount:  runResult.GeneratedRiderCount,
			GeneratedOrderCount:  int(pipelineResult.GeneratedOrders),
			GeneratedWithinWindow: int(pipelineResult.GeneratedWithinWindow),
			AdmittedOrderCount:   int(pipelineResult.AdmittedOrders),
			AdmittedWithinWindow: int(pipelineResult.AdmittedWithinWindow),
			CompletedOrderCount:  int(pipelineResult.CompletedOrders),
			CompletedWithinWindow: int(pipelineResult.CompletedWithinWindow),
			UnfinishedOrderCount: int(pipelineResult.UnfinishedOrders),
			LastPlannedArrivalNs: pipelineResult.LastPlannedArrivalNs,
			MaxQueueDepth:        pipelineResult.MaxQueueDepth,
			WorkerCompleted:      pipelineResult.WorkerCompleted,
			GOMAXPROCS:           runResult.GOMAXPROCS,
			MemoryEstimate:       memoryEstimate,
			BalancedMemory:       balancedMemory,
			Index:                runResult.Index,
			Timing: timingOutput{
				RiderGenerationNs: riderGenerationDuration.Nanoseconds(),
				IndexBuildNs:      indexBuildDuration.Nanoseconds(),
				PipelineNs:        pipelineDuration.Nanoseconds(),
				TotalNs:           totalDuration.Nanoseconds(),
				ThroughputPerSec:  throughput,
			},
			Performance:       pipelineResult.Performance,
			Resources:         resourceStats,
			RiderPreview:      displayRiders(runResult.RiderPreview, previewSize),
			OrderPreview:      displayOrders(pipelineResult.OrderPreview),
			AssignmentPreview: displayAssignments(pipelineResult.AssignmentPreview),
			Report:            pipelineResult.Report,
			StrategyDetails:   pipelineResult.StrategyDetails,
			ReadableSummary: readableSummary{
				Workload: workloadText,
				Result: resultText,
				Strategy: strategyText,
				Timing: timingText,
				Load: loadText,
				Distance: distanceText,
				Algorithm: algorithmText,
				Resources: resourceText,
			},
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
