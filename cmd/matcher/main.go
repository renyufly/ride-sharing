// Command matcher is the standalone entry point for the matching exercise.
//
// Step three generates deterministic data and applies the serial brute-force
// nearest-rider baseline.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ride-sharing/internal/config"
	"ride-sharing/internal/generator"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/matcher/bruteforce"
	"ride-sharing/internal/model"
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
	MatchedOrderCount    int                `json:"matchedOrderCount"`
	LastPlannedArrivalNs int64              `json:"lastPlannedArrivalNs"`
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
	if cfg.Algorithm != config.AlgorithmBruteForce {
		fmt.Fprintf(os.Stderr, "algorithm %q is not implemented in step three\n", cfg.Algorithm)
		os.Exit(2)
	}
	nearestMatcher, err := bruteforce.New(riders)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create brute-force matcher: %v\n", err)
		os.Exit(1)
	}
	reporter, err := report.New(riders)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create assignment reporter: %v\n", err)
		os.Exit(1)
	}
	stream, err := dataGenerator.NewOrderStream(cfg.OrderCount, cfg.ArrivalWindow, cfg.ArrivalModel, cfg.OrderDistribution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create order stream: %v\n", err)
		os.Exit(1)
	}

	const previewSize = 3
	orderPreview := make([]orderOutput, 0, previewSize)
	assignmentPreview := make([]assignmentOutput, 0, previewSize)
	generatedOrders := 0
	matchedOrders := 0
	var lastPlannedArrival int64
	for {
		order, ok, err := stream.Next()
		if err != nil {
			fmt.Fprintf(os.Stderr, "generate order: %v\n", err)
			os.Exit(1)
		}
		if !ok {
			break
		}
		generatedOrders++
		lastPlannedArrival = order.PlannedArrivalNs
		assignment, err := nearestMatcher.Match(order)
		if err != nil {
			fmt.Fprintf(os.Stderr, "match order sequence %d: %v\n", order.Sequence, err)
			os.Exit(1)
		}
		matchedOrders++
		if err := reporter.Observe(assignment); err != nil {
			fmt.Fprintf(os.Stderr, "record assignment for sequence %d: %v\n", order.Sequence, err)
			os.Exit(1)
		}
		if len(orderPreview) < previewSize {
			orderPreview = append(orderPreview, displayOrder(order))
			assignmentPreview = append(assignmentPreview, displayAssignment(assignment))
		}
	}

	bounds := dataGenerator.Bounds()
	seeds := dataGenerator.Seeds()
	runReport := reporter.Summary()
	if runReport.AssignmentCount != uint64(generatedOrders) || runReport.RiderOrderCountSum != uint64(matchedOrders) {
		fmt.Fprintf(
			os.Stderr,
			"assignment conservation failed: generated=%d matched=%d assignments=%d riderCountSum=%d\n",
			generatedOrders,
			matchedOrders,
			runReport.AssignmentCount,
			runReport.RiderOrderCountSum,
		)
		os.Exit(1)
	}
	output := startupOutput{
		Phase:   "serial-brute-force-report",
		Message: "all orders matched and summarized; KD-tree and concurrency are not implemented in step four",
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
			GeneratedOrderCount:  generatedOrders,
			MatchedOrderCount:    matchedOrders,
			LastPlannedArrivalNs: lastPlannedArrival,
			RiderPreview:         displayRiders(riders, previewSize),
			OrderPreview:         orderPreview,
			AssignmentPreview:    assignmentPreview,
			Report:               runReport,
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

func displayAssignment(assignment model.Assignment) assignmentOutput {
	return assignmentOutput{
		OrderID:               assignment.OrderID,
		Sequence:              assignment.Sequence,
		RiderUID:              assignment.RiderUID,
		DistanceSquaredMeters: assignment.DistanceSquaredMeters,
	}
}

func displayGeoPoint(point model.GeoPoint) geoPointOutput {
	return geoPointOutput{Latitude: point.Latitude, Longitude: point.Longitude}
}

func displayPoint(point model.Point2D) pointOutput {
	return pointOutput{X: point.X, Y: point.Y}
}
