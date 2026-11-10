package run

import (
	"context"
	"errors"
	"fmt"
	"sort"

	deferredmemory "ride-sharing/internal/adapter/deferred/memory"
	appdispatch "ride-sharing/internal/app/dispatch"
	appmatching "ride-sharing/internal/app/matching"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/platform/config"
	"time"
)

type LiveResult struct {
	InputCount      uint64 // 成功读取的订单数
	AcceptedCount   uint64 // 成功被骑手接受的订单数
	UnassignedCount uint64
	Assignments     []matching.Assignment    // 成功分配的订单记录
	Attempts        []domaindispatch.Attempt // 骑手接单尝试记录
	Orders          []order.Order            `json:"orders"` // 地图展示本次订单位置
	Pipeline        LivePipelineSummary      `json:"pipeline"`
}

// LivePipelineSummary exposes the shared matching pipeline used by a live run.
// Worker counts are measured results; pool sizes describe the run configuration.
type LivePipelineSummary struct {
	CandidateWorkers         int      `json:"candidateWorkers"`
	OfferWorkers             int      `json:"offerWorkers"`
	BatchSize                int      `json:"batchSize"`
	BatchPoolCapacity        int      `json:"batchPoolCapacity"`
	CandidatePoolCapacity    int      `json:"candidatePoolCapacity"`
	TopK                     int      `json:"topK"`
	CandidateWorkerCompleted []uint64 `json:"candidateWorkerCompleted"`
	MaxCandidateQueueDepth   int      `json:"maxCandidateQueueDepth"`
	MaxReorderDepth          int      `json:"maxReorderDepth"`
}

// liveBrowserOrderSource gives generated live-demo orders IDs that survive
// JavaScript JSON number parsing and can be echoed back over WebSocket.
// The normal simulation generator keeps its original 64-bit IDs.
type liveBrowserOrderSource struct {
	source appmatching.OrderSource
	base   uint64
}

func (s liveBrowserOrderSource) Next() (order.Order, bool, error) {
	value, ok, err := s.source.Next()
	if !ok || err != nil {
		return value, ok, err
	}
	value.ID = s.base + value.Sequence + 1
	return value, true, nil
}

// RunLive 实时派单，逐单派送
// dispathcer 查询候选骑手
// sender	向骑手发送接单请求
func RunLive(
	ctx context.Context,
	cfg config.Config,
	factory InputFactory,
	source appmatching.OrderSource,
	dispatcher *appdispatch.Service,
	sender appdispatch.Sender,
	responseTimeout time.Duration,
	ledger appmatching.OfferLedger,
) (result LiveResult, runErr error) {

	if ctx == nil || factory == nil || dispatcher == nil || sender == nil {
		return result, errors.New("live run dependencies cannot be nil")
	}
	if responseTimeout <= 0 {
		return result, errors.New("response timeout must be positive")
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	if cfg.Strategy != config.StrategyBalanced {
		return result, errors.New("live run requires balanced strategy")
	}

	if cfg.RunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.RunTimeout)
		defer cancel()
	}

	prepared, err := factory.Prepare(cfg, source)
	if err != nil {
		return result, err
	}
	if source == nil {
		const sequenceBits = 24
		if prepared.ExpectedOrders >= 1<<sequenceBits {
			return result, errors.New("generated live order count exceeds browser-safe ID range")
		}
		prepared.OrderSource = liveBrowserOrderSource{
			source: prepared.OrderSource,
			base:   (uint64(cfg.Seed) & ((1 << 29) - 1)) << sequenceBits,
		}
	}
	if prepared.SourceCloser != nil {
		defer func() {
			runErr = errors.Join(runErr, prepared.SourceCloser.Close())
		}()
	}

	options := appmatching.BalancedOptions{
		Options: appmatching.Options{
			Workers:           prepared.Config.Workers,
			BatchSize:         prepared.Config.BatchSize,
			ChannelCapacity:   prepared.Config.ChannelCapacity,
			ExpectedOrders:    prepared.ExpectedOrders,
			PreviewSize:       prepared.Config.OrderCount,
			ObservationWindow: prepared.Config.ArrivalWindow,
		},
		TopK:                   prepared.Config.TopK,
		MaxExtraDistanceMeters: prepared.Config.MaxExtraDistanceMeters,
		MaxOrdersPerRider:      prepared.Config.MaxOrdersPerRider,
		AssignmentWindow:       0,
		Attempt:                uint32(prepared.Config.Attempt),
	}

	deferredSink, err := deferredmemory.NewMemorySink(0)
	if err != nil {
		return result, err
	}

	notifier, ok := sender.(appdispatch.AssignmentNotifier)
	if !ok {
		return result, errors.New("live sender cannot confirm assignments")
	}

	confirmer := liveOfferConfirmer{
		dispatcher:      dispatcher,
		sender:          sender,
		responseTimeout: responseTimeout,
		notifier:        notifier,
	}

	pipelineResult, err := appmatching.RunBalancedWithOffers(
		ctx,
		prepared.OrderSource,
		prepared.Matcher,
		prepared.Riders,
		options,
		deferredSink,
		confirmer,
		ledger,
	)

	result = LiveResult{
		InputCount:      pipelineResult.GeneratedOrders,
		AcceptedCount:   pipelineResult.CompletedOrders,
		UnassignedCount: pipelineResult.DeferredOrders,
		Assignments:     append([]matching.Assignment(nil), pipelineResult.AssignmentPreview...),
		Attempts:        append([]domaindispatch.Attempt(nil), pipelineResult.Attempts...),
		Orders:          append([]order.Order(nil), pipelineResult.OrderPreview...),
		Pipeline: LivePipelineSummary{
			CandidateWorkers:         prepared.Config.Workers,
			OfferWorkers:             prepared.Config.Workers,
			BatchSize:                prepared.Config.BatchSize,
			BatchPoolCapacity:        prepared.Config.ChannelCapacity + prepared.Config.Workers,
			CandidatePoolCapacity:    options.ReorderWindow(),
			TopK:                     prepared.Config.TopK,
			CandidateWorkerCompleted: append([]uint64(nil), pipelineResult.WorkerCompleted...),
		},
	}
	if details := pipelineResult.StrategyDetails; details != nil {
		result.Pipeline.MaxCandidateQueueDepth = details.MaxCandidateQueueDepth
		result.Pipeline.MaxReorderDepth = details.MaxReorderDepth
	}

	// 网络回复可能乱序到达；对外按订单 Sequence 返回。
	sort.Slice(result.Assignments, func(i, j int) bool {
		if result.Assignments[i].Sequence != result.Assignments[j].Sequence {
			return result.Assignments[i].Sequence < result.Assignments[j].Sequence
		}
		return result.Assignments[i].OrderID < result.Assignments[j].OrderID
	})

	if err != nil {
		return result, fmt.Errorf("run live matching: %w", err)
	}
	if result.InputCount != prepared.ExpectedOrders {
		return result, fmt.Errorf(
			"order source ended after %d orders; expected %d",
			result.InputCount,
			prepared.ExpectedOrders,
		)
	}

	return result, nil

}
