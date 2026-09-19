// Command matcher is the standalone entry point for the matching exercise.
//
// Step six runs exact nearest-rider matching through a bounded CSP pipeline.
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

type startupOutput struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Config  config.DisplayConfig `json:"config"`
	Data    dataOutput           `json:"data"`
}

type dataOutput struct {
	Bounds               boundsOutput                `json:"bounds"`
	ProjectionOrigin     geoPointOutput              `json:"projectionOrigin"`
	EarthRadiusMeters    float64                     `json:"earthRadiusMeters"`
	RiderSeed            int64                       `json:"riderSeed"`
	OrderSeed            int64                       `json:"orderSeed"`
	GeneratedRiderCount  int                         `json:"generatedRiderCount"`
	GeneratedOrderCount  int                         `json:"generatedOrderCount"`
	AdmittedOrderCount   int                         `json:"admittedOrderCount"`
	MatchedOrderCount    int                         `json:"matchedOrderCount"`
	UnfinishedOrderCount int                         `json:"unfinishedOrderCount"`
	LastPlannedArrivalNs int64                       `json:"lastPlannedArrivalNs"`
	MaxQueueDepth        int                         `json:"maxQueueDepthBatches"`
	WorkerCompleted      []uint64                    `json:"workerCompletedOrders"`
	GOMAXPROCS           int                         `json:"gomaxprocs"`
	MemoryEstimate       pipeline.MemoryEstimate     `json:"memoryEstimate"`
	Index                matchrule.IndexStats        `json:"index"`
	Timing               timingOutput                `json:"timing"`
	Performance          pipeline.PerformanceMetrics `json:"performance"`
	Resources            resource.Stats              `json:"resources"`
	RiderPreview         []riderOutput               `json:"riderPreview"`
	OrderPreview         []orderOutput               `json:"orderPreview"`
	AssignmentPreview    []assignmentOutput          `json:"assignmentPreview"`
	Report               report.Summary              `json:"report"`
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

type nearestMatcher interface {
	Match(model.Order) (model.Assignment, error)
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
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid matcher configuration: %v\n", err)
		os.Exit(2)
	}
	runtime.GC()
	totalStarted := time.Now()
	resourceMonitor, err := resource.Start(cfg.MonitorInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start resource monitor: %v\n", err)
		os.Exit(1)
	}

	riderGenerationStarted := time.Now()
	dataGenerator, err := generator.New(cfg.Seed, geo.SanFranciscoBounds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create data generator: %v\n", err)
		os.Exit(1)
	}
	riders, err := dataGenerator.GenerateRiders(cfg.RiderCount, cfg.RiderDistribution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate riders: %v\n", err)
		os.Exit(1)
	}
	riderGenerationDuration := time.Since(riderGenerationStarted)
	indexBuildStarted := time.Now()
	var selectedMatcher nearestMatcher
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
	indexBuildDuration := time.Since(indexBuildStarted)
	stream, err := dataGenerator.NewOrderStream(cfg.OrderCount, cfg.ArrivalWindow, cfg.ArrivalModel, cfg.OrderDistribution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create order stream: %v\n", err)
		os.Exit(1)
	}

	const previewSize = 3
	runContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	if cfg.RunTimeout > 0 {
		var cancelTimeout context.CancelFunc
		runContext, cancelTimeout = context.WithTimeout(runContext, cfg.RunTimeout)
		defer cancelTimeout()
	}
	pipelineOptions := pipeline.Options{
		Workers:         cfg.Workers,
		BatchSize:       cfg.BatchSize,
		ChannelCapacity: cfg.ChannelCapacity,
		ExpectedOrders:  uint64(cfg.OrderCount),
		PreviewSize:     previewSize,
	}
	memoryEstimate, err := pipeline.EstimateMemory(cfg.RiderCount, pipelineOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "estimate pipeline memory: %v\n", err)
		os.Exit(1)
	}
	pipelineStarted := time.Now()
	pipelineResult, err := pipeline.Run(runContext, stream, selectedMatcher, riders, pipelineOptions)
	pipelineDuration := time.Since(pipelineStarted)
	resourceStats := resourceMonitor.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	totalDuration := time.Since(totalStarted)
	throughput := 0.0
	if pipelineDuration > 0 {
		throughput = float64(pipelineResult.CompletedOrders) / pipelineDuration.Seconds()
	}

	bounds := dataGenerator.Bounds()
	seeds := dataGenerator.Seeds()
	output := startupOutput{
		Phase:   fmt.Sprintf("csp-%s-report", cfg.Algorithm),
		Message: "all orders matched exactly through the bounded CSP pipeline",
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
		},
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "write startup output: %v\n", err)
		os.Exit(1)
	}
}

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
