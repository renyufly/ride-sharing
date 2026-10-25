package run

import (
	"context"
	"errors"
	"fmt"
	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/config"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/rider"

	"ride-sharing/internal/resource"
	"runtime"
	"time"
)

func previewRiders(riders []rider.Rider, limit int) []rider.Rider {
	if limit <= 0 {
		return nil
	}

	if limit > len(riders) {
		limit = len(riders)
	}

	rider := make([]rider.Rider, limit)

	copy(rider, riders[:limit])

	return rider

}

func Run(ctx context.Context, req Request, factory InputFactory) (ret Result, resultErr error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("context cannot be nil")
	}
	if factory == nil {
		return Result{}, fmt.Errorf("input factory cannot be nil")
	}

	if err := req.Config.Validate(); err != nil {
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

	prepared, err := factory.Prepare(effectiveConfig, req.OrderSource)
	if err != nil {
		return ret, err
	}
	effectiveConfig = prepared.Config
	riders := prepared.Riders
	selectMatcher := prepared.Matcher
	orderSource := prepared.OrderSource
	expectedOrders := prepared.ExpectedOrders
	sourceCloser := prepared.SourceCloser

	if sourceCloser != nil {
		defer func() {
			if err := sourceCloser.Close(); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}()
	}

	ret = Result{
		Config:              effectiveConfig,
		Bounds:              prepared.Bounds,
		ProjectionOrigin:    prepared.ProjectionOrigin,
		Seeds:               prepared.Seeds,
		EarthRadiusMeter:    geo.EarthRadiusMeters,
		GeneratedRiderCount: len(riders),
		RiderPreview:        previewRiders(riders, req.PreviewSize),
		Index:               selectMatcher.IndexStats(),
		GOMAXPROCS:          runtime.GOMAXPROCS(0),
		Timing: Timing{
			RiderGeneration: prepared.RiderGeneration,
			IndexBuild:      prepared.IndexBuild,
		},
	}

	options := appmatching.Options{
		Workers:           effectiveConfig.Workers,
		BatchSize:         effectiveConfig.BatchSize,
		ChannelCapacity:   effectiveConfig.ChannelCapacity,
		ExpectedOrders:    expectedOrders,
		PreviewSize:       req.PreviewSize,
		ObservationWindow: effectiveConfig.ArrivalWindow,
	}

	var pipelineResult appmatching.Result
	var pipelineError error
	var pipelineDuration time.Duration
	var pipelineOperation string

	switch effectiveConfig.Strategy {
	case config.StrategyNearest:
		memoryEsti, err := appmatching.EstimateMemory(effectiveConfig.RiderCount, options)
		if err != nil {
			return ret, fmt.Errorf("estimate nearest pipeline memory: %w", err)
		}

		ret.MemoryEstimate = &memoryEsti

		pipelineStarted := time.Now()

		pipelineResult, pipelineError = appmatching.Run(runContext, orderSource, selectMatcher, riders, options)

		pipelineDuration = time.Since(pipelineStarted)

		pipelineOperation = "nearest"

	case config.StrategyBalanced:
		balancedOptions := appmatching.BalancedOptions{
			Options:                options,
			TopK:                   effectiveConfig.TopK,
			MaxExtraDistanceMeters: effectiveConfig.MaxExtraDistanceMeters,
			MaxOrdersPerRider:      effectiveConfig.MaxOrdersPerRider,
			AssignmentWindow:       effectiveConfig.ArrivalWindow,
			Attempt:                uint32(effectiveConfig.Attempt),
		}

		memoryEsti, err := appmatching.EstimateBalancedMemory(effectiveConfig.RiderCount, balancedOptions)

		if err != nil {
			return ret, fmt.Errorf("estimate balanced pipeline memory: %w", err)
		}
		ret.BalancedMemory = &memoryEsti

		pipelineOperation = "balanced"

		pipelineStart := time.Now()

		// 只使用 Request 中注入的 sink
		pipelineResult, pipelineError = appmatching.RunBalanced(runContext, orderSource, selectMatcher, riders, balancedOptions, req.DeferredSink)

		pipelineDuration = time.Since(pipelineStart)

	default:
		return ret, fmt.Errorf("unsupported strategy")
	}

	ret.Timing.Pipeline = pipelineDuration

	ret.Pipeline = pipelineResult

	if pipelineError != nil {
		return ret, fmt.Errorf("run %s pipeline: %w", pipelineOperation, pipelineError)
	}

	if pipelineDuration > 0 {
		throughput := float64(pipelineResult.CompletedOrders) / pipelineDuration.Seconds()
		ret.Timing.ThroughputPerSecond = throughput
	}

	ret.Resources = resourceMonitor.Stop()
	monitorStopped = true
	ret.Timing.Total = time.Since(totalstarted)

	return ret, nil
}
