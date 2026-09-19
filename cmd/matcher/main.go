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

	"ride-sharing/internal/config"
	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/matcher/bruteforce"
	"ride-sharing/internal/matcher/kdtree"
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/report"
)

type startupOutput struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Config  config.DisplayConfig `json:"config"`
	Data    dataOutput           `json:"data"`
}

type dataOutput struct {
	Bounds               boundsOutput       `json:"bounds"`
	ProjectionOrigin     geoPointOutput     `json:"projectionOrigin"`
	EarthRadiusMeters    float64            `json:"earthRadiusMeters"`
	RiderSeed            int64              `json:"riderSeed"`
	OrderSeed            int64              `json:"orderSeed"`
	GeneratedRiderCount  int                `json:"generatedRiderCount"`
	GeneratedOrderCount  int                `json:"generatedOrderCount"`
	AdmittedOrderCount   int                `json:"admittedOrderCount"`
	MatchedOrderCount    int                `json:"matchedOrderCount"`
	UnfinishedOrderCount int                `json:"unfinishedOrderCount"`
	LastPlannedArrivalNs int64              `json:"lastPlannedArrivalNs"`
	MaxQueueDepth        int                `json:"maxQueueDepthBatches"`
	WorkerCompleted      []uint64           `json:"workerCompletedOrders"`
	GOMAXPROCS           int                `json:"gomaxprocs"`
	RiderPreview         []riderOutput      `json:"riderPreview"`
	OrderPreview         []orderOutput      `json:"orderPreview"`
	AssignmentPreview    []assignmentOutput `json:"assignmentPreview"`
	Report               report.Summary     `json:"report"`
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
}

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid matcher configuration: %v\n", err)
		os.Exit(2)
	}

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
	pipelineResult, err := pipeline.Run(runContext, stream, selectedMatcher, riders, pipeline.Options{
		Workers:         cfg.Workers,
		BatchSize:       cfg.BatchSize,
		ChannelCapacity: cfg.ChannelCapacity,
		ExpectedOrders:  uint64(cfg.OrderCount),
		PreviewSize:     previewSize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
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
			RiderPreview:         displayRiders(riders, previewSize),
			OrderPreview:         displayOrders(pipelineResult.OrderPreview),
			AssignmentPreview:    displayAssignments(pipelineResult.AssignmentPreview),
			Report:               pipelineResult.Report,
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
