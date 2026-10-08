package matcherapi

import (
	"fmt"
	"ride-sharing/internal/config"
	"ride-sharing/internal/generator"
	"ride-sharing/internal/matchrun"
	"time"
)

// 前端传给后端什么

const maxPreviewSize = 500
const maxHttpRunTimeOut = 2*time.Minute // 120s = 2min

// 定义一个专用 HTTP 请求类型，只接收页面允许控制的参数

type Request struct{
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
}


type RetryRequest struct {
	RunID	string 	`json:"runId"`
	SourceRound int	`json:"sourceRound"`
	MaxOrdersPerRider int	`json:"maxOrdersPerRider"`
	GracePeriodMs int64	`json:"gracePeriodMs"`
}

func (r Request) toRunRequest() (matchrun.Request, error) {
	if r.PreviewSize < 0 {
		return matchrun.Request{}, fmt.Errorf("preview must >= 0")
	}

	if r.PreviewSize > maxPreviewSize {
		return matchrun.Request{}, fmt.Errorf("exceed the max preview size: %d", maxPreviewSize)
	}

	if r.MaxOrdersPerRider < 0 {
		return matchrun.Request{}, fmt.Errorf("maxOrdersPerRider cannot be negative")
	}

	arrivalWindow, err := time.ParseDuration(r.ArrivalWindow) 
	if err != nil {
		return matchrun.Request{}, fmt.Errorf("parse arrivalWindow: %w", err)
	}

	cfg := config.Default()

	cfg.RiderCount = r.RiderCount
	cfg.OrderCount = r.OrderCount
	cfg.ArrivalWindow = arrivalWindow
	cfg.Seed = r.Seed
	cfg.Workers = r.Workers
	cfg.BatchSize = r.BatchSize
	cfg.ChannelCapacity = r.ChannelCapacity
	cfg.TopK = r.TopK
	cfg.MaxExtraDistanceMeters = r.MaxExtraDistanceMeters
	cfg.MaxOrdersPerRider = r.MaxOrdersPerRider

	cfg.RunTimeout = maxHttpRunTimeOut //


	cfg.Algorithm = config.Algorithm(r.Algorithm)
	cfg.Strategy = config.Strategy(r.Strategy)
	cfg.ArrivalModel = generator.ArrivalModel(r.ArrivalModel)
	cfg.OrderDistribution = generator.SpatialDistribution(r.OrderDistribution)
	cfg.RiderDistribution = generator.SpatialDistribution(r.RiderDistribution)


	if err := cfg.Validate(); err != nil {
		return matchrun.Request{}, fmt.Errorf("validate matcher request: %w", err)
	}

	return matchrun.Request{
		Config: cfg,
		PreviewSize: r.PreviewSize,
	}, nil

}