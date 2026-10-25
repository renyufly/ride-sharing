package run

import (
	"io"
	"ride-sharing/internal/app/report"
	"time"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/rider"
	"ride-sharing/internal/platform/config"
)

// Matcher supplies both strategies over one static rider set.
type Matcher interface {
	appmatching.Matcher
	appmatching.CandidateMatcher
	IndexStats() report.IndexStats
}

// Seeds identifies the independent rider and order random streams.
type Seeds struct {
	Rider int64
	Order int64
}

// Prepared holds the data and search implementation for one run.
type Prepared struct {
	Config           config.Config
	Bounds           geo.BoundingBox
	ProjectionOrigin geo.GeoPoint
	Seeds            Seeds
	Riders           []rider.Rider
	Matcher          Matcher
	OrderSource      appmatching.OrderSource
	ExpectedOrders   uint64
	SourceCloser     io.Closer
	RiderGeneration  time.Duration
	IndexBuild       time.Duration
}

// InputFactory assembles concrete inputs without coupling the run use case to adapters.
type InputFactory interface {
	Prepare(config.Config, appmatching.OrderSource) (Prepared, error)
}
