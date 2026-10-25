package run_test

import (
	"context"
	"testing"
	"time"

	"ride-sharing/internal/adapter/deferred/memory"
	"ride-sharing/internal/adapter/simulation/runsetup"
	"ride-sharing/internal/app/run"
	"ride-sharing/internal/platform/config"
)

func TestRunKeepsNearestAndBalancedAssignmentCounts(t *testing.T) {
	cfg := config.Default()
	cfg.RiderCount = 2
	cfg.OrderCount = 5
	cfg.ArrivalWindow = time.Second
	cfg.ArrivalModel = config.ArrivalUnbounded
	result, err := run.Run(context.Background(), run.Request{Config: cfg, PreviewSize: 5}, runsetup.Factory{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pipeline.CompletedOrders != 5 || result.Pipeline.DeferredOrders != 0 || result.Pipeline.Report.AssignmentCount != 5 {
		t.Fatalf("nearest counts changed: %+v", result.Pipeline)
	}

	cfg.Strategy = config.StrategyBalanced
	cfg.TopK = 2
	cfg.MaxExtraDistanceMeters = 100000
	cfg.MaxOrdersPerRider = 1
	sink, err := memory.NewMemorySink(5)
	if err != nil {
		t.Fatal(err)
	}
	result, err = run.Run(context.Background(), run.Request{Config: cfg, DeferredSink: sink}, runsetup.Factory{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pipeline.CompletedOrders != 2 || result.Pipeline.DeferredOrders != 3 || sink.Snapshot().Total != 3 {
		t.Fatalf("balanced counts changed: completed=%d deferred=%d sink=%d", result.Pipeline.CompletedOrders, result.Pipeline.DeferredOrders, sink.Snapshot().Total)
	}
}
