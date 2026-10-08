package matchrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"ride-sharing/internal/config"
	"ride-sharing/internal/deferred"
	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/matcher/bruteforce"
	"ride-sharing/internal/matcher/kdtree"

	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/resource"
	"runtime"
	"time"
)

type nearestMatcher interface {  
	Match(model.Order) (model.Assignment, error)
	TopKInto(model.Order, int, []matchrule.Candidate) ([]matchrule.Candidate, error)
	IndexStats() matchrule.IndexStats
}

func createMatcher(cfg config.Config, riders []model.Rider) (nearestMatcher, error) {
	var selectedMatcher nearestMatcher
	var err error
	switch cfg.Algorithm {
	case config.AlgorithmBruteForce:
		selectedMatcher, err = bruteforce.New(riders)
	case config.AlgorithmKDTree:
		selectedMatcher, err = kdtree.New(riders)
	default:
		err = fmt.Errorf("unsupported algorithm %q", cfg.Algorithm)
	}

	return selectedMatcher, err

}

// 订单源辅助函数
func createOrderSource (cfg *config.Config, dataGenerator generator.Generator) (pipeline.OrderSource, uint64, io.Closer, error) {
	
	if cfg.DeferredInput == "" {
		generatedStream, err := dataGenerator.NewOrderStream(cfg.OrderCount, cfg.ArrivalWindow, cfg.ArrivalModel, cfg.OrderDistribution)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("create generated order stream: %w", err)
		}

		return generatedStream, uint64(cfg.OrderCount), nil, nil
	}

	if cfg.Attempt <= 1 {
		return nil, 0, nil, fmt.Errorf("deferred input requires attempt greater than one")
	}
	expectedInputAttempt := cfg.Attempt - 1

	filesource, count, err := deferred.OpenFileSource(cfg.DeferredInput, uint32(expectedInputAttempt))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%w", err)
	}

	// 
	cfg.OrderCount = int(count)

	// 调用者通过第一个接口读取订单，通过第三个接口负责最终关闭文件
	return filesource, count, filesource, nil

}

func previewRiders(riders []model.Rider, limit int) []model.Rider {
	if limit <= 0 {
		return nil
	}

	if limit > len(riders) {
		limit = len(riders)
	}

	rider := make([]model.Rider, limit)

	copy(rider, riders[:limit])

	return rider

}

func Run(ctx context.Context, req Request) (ret Result, resultErr error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("context cannot be nil")
	}

	if err:= req.Config.Validate(); err != nil {
		return Result{}, fmt.Errorf("validate matcher run configuration: %w", err)
	}

	if req.PreviewSize < 0 {
		return Result{}, fmt.Errorf("wrong preview size")
	}

	if req.Config.Strategy == config.StrategyBalanced && req.DeferredSink == nil {
		return Result{}, fmt.Errorf("empty deferred sink")
	}

	// Runner 内只能修改和使用 effectiveConfig
	effectiveConfig := req.Config

	runContext := ctx

	if effectiveConfig.RunTimeout > 0 {
		var cancel context.CancelFunc
		runContext, cancel = context.WithTimeout(ctx, effectiveConfig.RunTimeout)
		defer cancel()
	}

	runtime.GC()
	totalstarted := time.Now()

	resourceMonitor, err := resource.Start(effectiveConfig.MonitorInterval)
	if err != nil {
		return Result{}, err
	}

	monitorStopped := false

	defer func() {
		if !monitorStopped {
			ret.Resources = resourceMonitor.Stop()
			monitorStopped = true
		}
	}()

	// TODO
	riderGenerationStarted := time.Now()

	dataGenerator , err := generator.New(effectiveConfig.Seed, geo.SanFranciscoBounds)
	if err != nil {
		return Result{}, fmt.Errorf("%w", err)
	}

	// 生成 Riders
	riders, err := dataGenerator.GenerateRiders(effectiveConfig.RiderCount, effectiveConfig.RiderDistribution)
	if err != nil {
		return ret, fmt.Errorf("generate riders: %w", err)
	}

	riderGenerationDuration := time.Since(riderGenerationStarted)

	// 记录索引构建起点
	indexBuildStarted := time.Now()
	selectMatcher, err := createMatcher(effectiveConfig, riders)
	if err != nil {
		return Result{}, fmt.Errorf("%w", err)
	}

	indexBuildDuration := time.Since(indexBuildStarted)

	var orderSource pipeline.OrderSource
	var expectedOrders uint64
	var sourceCloser io.Closer

	if req.OrderSource != nil {
		if effectiveConfig.DeferredInput != "" {
			return ret, fmt.Errorf("order source and deferred input cannot both be set")
		}
		orderSource = req.OrderSource
		expectedOrders = uint64(effectiveConfig.OrderCount)
		sourceCloser = nil
	} else {
		orderSource, expectedOrders, sourceCloser, err = createOrderSource(&effectiveConfig, dataGenerator)
		if err != nil {
			return ret, fmt.Errorf("create order source: %w", err)
		}
	}
	

	if sourceCloser != nil {
		defer func() {
			if err := sourceCloser.Close(); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}()
	}


	ret = Result{
		Config: effectiveConfig,
		Bounds: dataGenerator.Bounds(),
		ProjectionOrigin: dataGenerator.Origin(),
		Seeds: dataGenerator.Seeds(),
		EarthRadiusMeter: geo.EarthRadiusMeters,
		GeneratedRiderCount: len(riders),
		RiderPreview: previewRiders(riders, req.PreviewSize),
		Index: selectMatcher.IndexStats(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		Timing: Timing{
			RiderGeneration: riderGenerationDuration,
			IndexBuild: indexBuildDuration,
		},
	}


	options := pipeline.Options{
		Workers: effectiveConfig.Workers,
		BatchSize: effectiveConfig.BatchSize,
		ChannelCapacity: effectiveConfig.ChannelCapacity,
		ExpectedOrders: expectedOrders,
		PreviewSize: req.PreviewSize,
		ObservationWindow: effectiveConfig.ArrivalWindow,
	}


	var pipelineResult pipeline.Result
	var pipelineError error
	var pipelineDuration time.Duration
	var pipelineOperation string

	switch effectiveConfig.Strategy {
	case config.StrategyNearest: 
		memoryEsti, err := pipeline.EstimateMemory(effectiveConfig.RiderCount, options)
		if err != nil {
			return ret, fmt.Errorf("estimate nearest pipeline memory: %w", err)
		}

		ret.MemoryEstimate = &memoryEsti

		pipelineStarted := time.Now()

		pipelineResult, pipelineError = pipeline.Run(runContext, orderSource, selectMatcher, riders, options)
		
		pipelineDuration = time.Since(pipelineStarted)
		


		pipelineOperation = "nearest"

	case config.StrategyBalanced:
		balancedOptions := pipeline.BalancedOptions{
			Options: options,
			TopK: effectiveConfig.TopK,
			MaxExtraDistanceMeters: effectiveConfig.MaxExtraDistanceMeters,
			MaxOrdersPerRider: effectiveConfig.MaxOrdersPerRider,
			AssignmentWindow: effectiveConfig.ArrivalWindow,
			Attempt: uint32(effectiveConfig.Attempt),
		}

		memoryEsti, err := pipeline.EstimateBalancedMemory(effectiveConfig.RiderCount, balancedOptions)
		
		if err != nil {
			return ret, fmt.Errorf("estimate balanced pipeline memory: %w", err)
		}
		ret.BalancedMemory = &memoryEsti

		pipelineOperation = "balanced"

		pipelineStart := time.Now()

		// 只使用 Request 中注入的 sink
		pipelineResult, pipelineError = pipeline.RunBalanced(runContext, orderSource, selectMatcher, riders, balancedOptions, req.DeferredSink)

		pipelineDuration = time.Since(pipelineStart)




	default:
		return ret, fmt.Errorf("unsupported strategy")
	}


	ret.Timing.Pipeline = pipelineDuration

	ret.Pipeline = pipelineResult

	if pipelineError != nil {
		return ret, fmt.Errorf("run %s pipeline: %w", pipelineOperation,pipelineError)
	}

	if pipelineDuration > 0 {
		throughput := float64(pipelineResult.CompletedOrders) / pipelineDuration.Seconds()
		ret.Timing.ThroughputPerSecond = throughput
	}


	ret.Resources = resourceMonitor.Stop()
	monitorStopped = true
	ret.Timing.Total = time.Since(totalstarted)

	return  ret, nil
}