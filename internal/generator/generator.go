// Package generator creates deterministic riders and a replayable, streaming
// sequence of orders without retaining all orders in memory.
package generator

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"ride-sharing/internal/geo"
	"ride-sharing/internal/model"
)

type ArrivalModel string

const (
	ArrivalUniformWindow    ArrivalModel = "uniform-window"
	ArrivalFrontLoadedBurst ArrivalModel = "front-loaded-burst"
	ArrivalUnbounded        ArrivalModel = "unbounded"
)

type SpatialDistribution string

const (
	DistributionUniform SpatialDistribution = "uniform"
	DistributionHotspot SpatialDistribution = "hotspot"
	DistributionSkewed  SpatialDistribution = "skewed"
)

var hotspotCenters = [...]struct {
	latitudeRatio  float64
	longitudeRatio float64
}{
	{latitudeRatio: 0.25, longitudeRatio: 0.30},
	{latitudeRatio: 0.70, longitudeRatio: 0.65},
	{latitudeRatio: 0.50, longitudeRatio: 0.80},
}

type Seeds struct {
	Rider int64
	Order int64
}

type Generator struct {
	bounds    geo.BoundingBox
	projector geo.Projector
	seeds     Seeds
}

func New(masterSeed int64, bounds geo.BoundingBox) (Generator, error) {
	if err := bounds.Validate(); err != nil {
		return Generator{}, fmt.Errorf("invalid generator bounds: %w", err)
	}
	projector, err := geo.NewProjector(bounds.Center())
	if err != nil {
		return Generator{}, err
	}

	return Generator{
		bounds:    bounds,
		projector: projector,
		seeds: Seeds{
			Rider: int64(mix64(uint64(masterSeed) ^ 0x7269646572736565)),
			Order: int64(mix64(uint64(masterSeed) ^ 0x6f72646572736565)),
		},
	}, nil
}

func (g Generator) Bounds() geo.BoundingBox {
	return g.bounds
}

func (g Generator) Origin() model.GeoPoint {
	return g.projector.Origin()
}

func (g Generator) Seeds() Seeds {
	return g.seeds
}

func (g Generator) GenerateRiders(count int, distribution SpatialDistribution) ([]model.Rider, error) {
	if count <= 0 {
		return nil, errors.New("rider count must be greater than zero")
	}
	if err := ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	random := rand.New(rand.NewSource(g.seeds.Rider))
	riders := make([]model.Rider, count)
	for index := range riders {
		location := samplePoint(random, g.bounds, distribution)
		point, err := g.projector.Project(location)
		if err != nil {
			return nil, fmt.Errorf("project rider %d: %w", index+1, err)
		}
		riders[index] = model.Rider{
			UID:      uint64(index + 1),
			Location: location,
			Point:    point,
		}
	}
	return riders, nil
}

// NewOrderStream returns a fresh replayable stream. Calling it again with the
// same arguments restarts the exact order sequence from Sequence 0.
func (g Generator) NewOrderStream(count int, window time.Duration, arrival ArrivalModel, distribution SpatialDistribution) (*OrderStream, error) {
	if count <= 0 {
		return nil, errors.New("order count must be greater than zero")
	}
	if window < 0 {
		return nil, errors.New("arrival window cannot be negative")
	}
	if err := ValidateArrivalModel(arrival); err != nil {
		return nil, err
	}
	if err := ValidateSpatialDistribution(distribution); err != nil {
		return nil, err
	}

	return &OrderStream{
		total:        uint64(count),
		window:       window,
		arrival:      arrival,
		distribution: distribution,
		bounds:       g.bounds,
		projector:    g.projector,
		orderSeed:    g.seeds.Order,
		random:       rand.New(rand.NewSource(g.seeds.Order)),
	}, nil
}

type OrderStream struct {
	nextSequence uint64
	total        uint64
	window       time.Duration
	arrival      ArrivalModel
	distribution SpatialDistribution
	bounds       geo.BoundingBox
	projector    geo.Projector
	orderSeed    int64
	random       *rand.Rand
}

// Next generates one order. It does not sleep until PlannedArrivalNs; the CSP
// pipeline added later owns pacing and admission-delay measurement.
func (s *OrderStream) Next() (model.Order, bool, error) {
	if s.nextSequence >= s.total {
		return model.Order{}, false, nil
	}

	sequence := s.nextSequence
	location := samplePoint(s.random, s.bounds, s.distribution)
	point, err := s.projector.Project(location)
	if err != nil {
		return model.Order{}, false, fmt.Errorf("project order sequence %d: %w", sequence, err)
	}

	order := model.Order{
		ID:               mix64(uint64(s.orderSeed) + sequence),
		Sequence:         sequence,
		PlannedArrivalNs: plannedArrival(sequence, s.total, s.window, s.arrival).Nanoseconds(),
		Pickup:           location,
		Point:            point,
	}
	s.nextSequence++
	return order, true, nil
}

func plannedArrival(sequence, total uint64, window time.Duration, arrival ArrivalModel) time.Duration {
	if arrival == ArrivalUnbounded || total <= 1 || window == 0 {
		return 0
	}
	if sequence == total-1 {
		return window
	}

	position := float64(sequence) / float64(total-1)
	if arrival == ArrivalFrontLoadedBurst {
		// Schedule 80% of orders in the first 20% of the window, then spread
		// the remaining 20% across the final 80%.
		if position <= 0.8 {
			position = position * 0.25
		} else {
			position = 0.2 + (position-0.8)*4
		}
	}
	return time.Duration(float64(window) * position)
}

func ValidateArrivalModel(arrival ArrivalModel) error {
	switch arrival {
	case ArrivalUniformWindow, ArrivalFrontLoadedBurst, ArrivalUnbounded:
		return nil
	default:
		return fmt.Errorf("unsupported arrival model %q", arrival)
	}
}

func ValidateSpatialDistribution(distribution SpatialDistribution) error {
	switch distribution {
	case DistributionUniform, DistributionHotspot, DistributionSkewed:
		return nil
	default:
		return fmt.Errorf("unsupported spatial distribution %q", distribution)
	}
}

func samplePoint(random *rand.Rand, bounds geo.BoundingBox, distribution SpatialDistribution) model.GeoPoint {
	latitudeSpan := bounds.MaxLatitude - bounds.MinLatitude
	longitudeSpan := bounds.MaxLongitude - bounds.MinLongitude

	var latitudeRatio, longitudeRatio float64
	switch distribution {
	case DistributionHotspot:
		if random.Float64() < 0.85 {
			center := hotspotCenters[random.Intn(len(hotspotCenters))]
			latitudeRatio = center.latitudeRatio + random.NormFloat64()*0.035
			longitudeRatio = center.longitudeRatio + random.NormFloat64()*0.035
		} else {
			latitudeRatio = random.Float64()
			longitudeRatio = random.Float64()
		}
	case DistributionSkewed:
		latitudeRatio = cube(random.Float64())
		longitudeRatio = cube(random.Float64())
	default:
		latitudeRatio = random.Float64()
		longitudeRatio = random.Float64()
	}

	latitudeRatio = clampUnit(latitudeRatio)
	longitudeRatio = clampUnit(longitudeRatio)
	return model.GeoPoint{
		Latitude:  bounds.MinLatitude + latitudeRatio*latitudeSpan,
		Longitude: bounds.MinLongitude + longitudeRatio*longitudeSpan,
	}
}

func cube(value float64) float64 {
	return value * value * value
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// mix64 is the SplitMix64 finalizer. It provides stable domain-derived seeds
// and a one-to-one mapping for deterministic order IDs.
func mix64(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}
