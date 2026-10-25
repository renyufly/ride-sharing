package replay

import (
	"fmt"
	"time"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/app/run"
	"ride-sharing/internal/config"
)

// Prepare builds the next whole-round run from deferred orders.
func Prepare(base config.Config, currentRound, deferredCount, capacity int, gracePeriodMs int64, previewSize int, source appmatching.OrderSource, sink appmatching.DeferredOrderSink) (run.Request, error) {
	if currentRound < 1 || deferredCount < 1 || source == nil || sink == nil {
		return run.Request{}, fmt.Errorf("invalid deferred replay inputs")
	}
	cfg := base
	cfg.OrderCount = deferredCount
	cfg.Attempt = uint(currentRound + 1)
	cfg.MaxOrdersPerRider = capacity
	cfg.DeferredInput = ""
	if gracePeriodMs > 0 {
		cfg.ArrivalWindow = time.Duration(gracePeriodMs) * time.Millisecond
	}
	return run.Request{Config: cfg, PreviewSize: previewSize, OrderSource: source, DeferredSink: sink}, nil
}
