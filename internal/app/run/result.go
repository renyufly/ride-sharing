package run

import (
	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/app/report"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/rider"
	"ride-sharing/internal/platform/config"
	"ride-sharing/internal/platform/metrics"
	"time"
)

type Result struct {
	Config              config.Config
	Bounds              geo.BoundingBox
	ProjectionOrigin    geo.GeoPoint
	Seeds               Seeds
	EarthRadiusMeter    float64
	GeneratedRiderCount int
	RiderPreview        []rider.Rider
	Pipeline            appmatching.Result
	Index               report.IndexStats
	MemoryEstimate      *appmatching.MemoryEstimate
	BalancedMemory      *appmatching.BalancedMemoryEstimate
	Resources           metrics.Stats
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
