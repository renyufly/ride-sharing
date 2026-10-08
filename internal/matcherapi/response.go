package matcherapi

import (
	"ride-sharing/internal/matcher"
	"ride-sharing/internal/pipeline"
	"ride-sharing/internal/report"
	"ride-sharing/internal/resource"
)

// 后端最终返回给前端什么
// API 的输出数据格式

// 成功响应-json
type RunResponse struct {
	// Result	matchrun.Result	`json:"result"`	 // Runner 的完整统计结果
	// OrderPreview	[]model.Order	`json:"orderPreview"`	// 地图上的订单抽样点
	// AssignmentPreview	[]model.Assignment	`json:"assignmentPreview"`	// 地图匹配连线
	// Deferred	deferred.MemorySnapshot    `json:"deferred"`  // 内存 sink 的累计数和抽样订单
	Config	RunConfigResponse	`json:"config"`
	Counts	RunCountsResponse	`json:"counts"`
	Timing	RunTimingResponse	`json:"timing"`
	Map	RunMapResponse	`json:"map"`
	Performance	pipeline.PerformanceMetrics	`json:"performance"`
	Fairness	report.Summary	`json:"fairness"`
	Resources	resource.Stats	`json:"resources"`
	Index	matcher.IndexStats	`json:"index"`
	Strategy	*pipeline.StrategyMetrics	`json:"strategy,omitempty"`
	Deferred	DeferredResponse	`json:"deferred"`

	RunID        string	`json:"runId"`
	CurrentRound int	`json:"currentRound"`
	Rounds       []RoundResponse	`json:"rounds"`
}

type RoundResponse struct {
	Round                 int 	`json:"round"`
	InputSource           string	`json:"inputSource"`
	SourceRound           int	`json:"sourceRound"`
	InputCount            uint64	`json:"inputCount"`
	MatchedCount          uint64	`json:"matchedCount"`
	DeferredTotal         uint64	`json:"deferredTotal"`
	DeferredCapacity      uint64	`json:"deferredCapacity"`
	DeferredWindow        uint64	`json:"deferredWindow"`
	MaxOrdersPerRider     int	`json:"maxOrdersPerRider"`
	GracePeriodMs         int64	`json:"gracePeriodMs"`
DeferredSamples       []DeferredOrderResponse	`json:"deferredSamples"`
}

type RunConfigResponse struct {
	RiderCount	int	`json:"riderCount"`
	OrderCount	int	`json:"orderCount"`
	ArrivalWindow	string	`json:"arrivalWindow"`
	ArrivalModel	string	`json:"arrivalModel"`
	Seed	int64	`json:"seed"`
	RiderDistribution	string	`json:"riderDistribution"`
	OrderDistribution	string	`json:"orderDistribution"`
	Algorithm	string	`json:"algorithm"`
	Strategy	string	`json:"strategy"`
	Workers	int	`json:"workers"`
	BatchSize	int	`json:"batchSize"`
	ChannelCapacity	int	`json:"channelCapacity"`
	TopK	int	`json:"topK"`
	MaxExtraDistanceMeters	float64	`json:"maxExtraDistanceMeters"`
	MaxOrdersPerRider	int	`json:"maxOrdersPerRider"`
	PreviewSize	int	`json:"previewSize"`
	Attempt	uint	`json:"attempt"`
}

type RunCountsResponse struct {
Generated	uint64 `json:"generated"`
Admitted	uint64 `json:"admitted"`
Completed	uint64 `json:"completed"`
Unfinished	uint64	`json:"unfinished"`
Deferred	uint64	`json:"deferred"`
DeferredByCapacity	uint64	`json:"deferredByCapacity"`
DeferredByWindow	uint64	`json:"deferredByWindow"`
GeneratedWithinWindow	uint64	`json:"generatedWithinWindow"`
AdmittedWithinWindow	uint64	`json:"admittedWithinWindow"`
CompletedWithinWindow	uint64	`json:"completedWithinWindow"`
CompletionConserved	bool `json:"completionConserved"`
DeferredConserved	bool `json:"deferredConserved"`
}

type RunTimingResponse struct {
	RiderGenerationNs	int64	`json:"riderGenerationNs"` //	Timing.RiderGeneration.Nanoseconds
	IndexBuildNs	int64	`json:"indexBuildNs"` //	Timing.IndexBuild.Nanoseconds
	PipelineNs	int64	`json:"pipelineNs"` //	Timing.Pipeline.Nanoseconds
	TotalNs	int64	`json:"totalNs"` //	Timing.Total.Nanoseconds
	ThroughputPerSecond	float64	`json:"throughputPerSecond"`
}

type GeoPointResponse struct {
	Latitude float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type BoundsResponse struct {
	MinLatitude	float64 `json:"minLatitude"`
	MaxLatitude	float64	`json:"maxLatitude"`
	MinLongitude	float64 `json:"minLongitude"`
	MaxLongitude	float64	`json:"maxLongitude"`
}

type RiderPreviewResponse struct {
	UID uint64 `json:"uid"`
	Location GeoPointResponse `json:"location"`
}

type OrderPreviewResponse struct {
	ID uint64 `json:"id"`
	Sequence uint64 `json:"sequence"`
	PlannedArrivalNs int64	`json:"plannedArrivalNs"`
	Pickup	GeoPointResponse `json:"pickup"`
}

type AssignmentPreviewResponse struct {
	OrderID	uint64 `json:"orderId"`
Sequence	uint64 `json:"sequence"`
RiderUID	uint64 `json:"riderUid"`
DistanceMeters	float64 `json:"distanceMeters"`
}

type RunMapResponse struct {
	Bounds BoundsResponse `json:"bounds"`
	Riders []RiderPreviewResponse	`json:"riders"`
	Orders []OrderPreviewResponse	`json:"orders"`
	Assignments []AssignmentPreviewResponse	`json:"assignments"`
}

type DeferredResponse struct {
Total	uint64	`json:"total"`
Orders	[]DeferredOrderResponse	`json:"orders"`
}

type DeferredOrderResponse struct {
Order OrderPreviewResponse `json:"order"`
Reason string	`json:"reason"`
DeferredAtNs int64	`json:"deferredAtNs"`
 Attempt uint32	`json:"attempt"`
}


type ErrorResponse struct {
	Error string `json:"error"`
}