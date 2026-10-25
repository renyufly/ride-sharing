package matchrun

import (
	"ride-sharing/internal/adapter/simulation"
	"ride-sharing/internal/config"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/rider"
	"ride-sharing/internal/matcher"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/resource"
	"time"
)

type Result struct {
	Config              config.Config
	Bounds              geo.BoundingBox
	ProjectionOrigin    geo.GeoPoint
	Seeds               simulation.Seeds
	EarthRadiusMeter    float64
	GeneratedRiderCount int
	RiderPreview        []rider.Rider
	Pipeline            pipeline.Result
	Index               matcher.IndexStats
	MemoryEstimate      *pipeline.MemoryEstimate
	BalancedMemory      *pipeline.BalancedMemoryEstimate
	Resources           resource.Stats
	Timing              Timing
	GOMAXPROCS          int
}

type Timing struct {
	RiderGeneration     time.Duration
	IndexBuild          time.Duration
	Pipeline            time.Duration
	Total               time.Duration
	ThroughputPerSecond float64
}
