package matcherapi

import (
	"math"
	"ride-sharing/internal/config"
	"ride-sharing/internal/deferred"
	"ride-sharing/internal/geo"
	"ride-sharing/internal/matchrun"
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
)


func toRunResponse(result matchrun.Result, snapshot deferred.MemorySnapshot, previewSize int, runid string, currentRound int, rounds []RoundResponse) RunResponse {
	return RunResponse{
		Config: mapRunConfig(result.Config, previewSize),
		Counts: mapRunCounts(result.Pipeline),
		Timing: mapRunTiming(result.Timing),
		Map: mapRunMap(result),
		Performance: result.Pipeline.Performance,
		Fairness: result.Pipeline.Report,
		Resources: result.Resources,
		Index: result.Index,
		Strategy: result.Pipeline.StrategyDetails,
		Deferred: mapDeferred(snapshot, previewSize),

		RunID: runid,
		CurrentRound: currentRound,
		Rounds: rounds,
	}
}

func mapRunConfig(cfg config.Config, previewSize int) RunConfigResponse {
	return RunConfigResponse{
		RiderCount: cfg.RiderCount,
		OrderCount: cfg.OrderCount,
		ArrivalWindow: cfg.ArrivalWindow.String(),
		ArrivalModel: string(cfg.ArrivalModel),
		Seed: cfg.Seed,
		RiderDistribution: string(cfg.RiderDistribution),
		OrderDistribution: string(cfg.OrderDistribution),
		Algorithm: string(cfg.Algorithm),
		Strategy: string(cfg.Strategy),
		Workers: cfg.Workers,
		BatchSize: cfg.BatchSize,
		ChannelCapacity: cfg.ChannelCapacity,
		TopK: cfg.TopK,
		MaxExtraDistanceMeters: cfg.MaxExtraDistanceMeters,
		MaxOrdersPerRider: cfg.MaxOrdersPerRider,
		PreviewSize: previewSize,
		Attempt: cfg.Attempt,
	}
}

func mapRunCounts(result pipeline.Result) RunCountsResponse {

	completionConserved := (result.GeneratedOrders == result.CompletedOrders + result.DeferredOrders)
	deferredConserved := (result.DeferredOrders == result.DeferredByCapacity + result.DeferredByWindow)
	
	return RunCountsResponse{
		Generated: result.GeneratedOrders,
		Admitted: result.AdmittedOrders,
		Completed: result.CompletedOrders,
		Unfinished: result.UnfinishedOrders,
		Deferred: result.DeferredOrders,
		DeferredByCapacity: result.DeferredByCapacity,
		DeferredByWindow: result.DeferredByWindow,
		GeneratedWithinWindow: result.GeneratedWithinWindow,
		AdmittedWithinWindow: result.AdmittedWithinWindow,
		CompletedWithinWindow: result.CompletedWithinWindow,

		CompletionConserved: completionConserved,
		DeferredConserved:deferredConserved,
	}

}

func mapRunTiming(timing matchrun.Timing) RunTimingResponse {
	return RunTimingResponse{
		RiderGenerationNs: timing.RiderGeneration.Nanoseconds(),
		IndexBuildNs: timing.IndexBuild.Nanoseconds(),
		PipelineNs: timing.Pipeline.Nanoseconds(),
		TotalNs: timing.Total.Nanoseconds(),
		ThroughputPerSecond: timing.ThroughputPerSecond,
	}
}

func mapGeoPoint(geo model.GeoPoint) GeoPointResponse {
	return GeoPointResponse{
		Latitude: geo.Latitude,
		Longitude: geo.Longitude,
	}
}

func mapBounds(bound geo.BoundingBox) BoundsResponse {
	return BoundsResponse{
		MinLatitude: bound.MinLatitude,
		MaxLatitude: bound.MaxLatitude,
		MinLongitude: bound.MinLongitude,
		MaxLongitude: bound.MaxLongitude,
	}
}

func mapRiders(riders []model.Rider) []RiderPreviewResponse {
	ridersResponse := make([]RiderPreviewResponse, 0)

	for _, rider := range riders{
		riderResponse := RiderPreviewResponse{
			UID: rider.UID,
			Location: GeoPointResponse(rider.Location),
		}
		ridersResponse = append(ridersResponse, riderResponse)
	}
	return ridersResponse
}

func mapOrder(order model.Order) OrderPreviewResponse {
	return OrderPreviewResponse{
		ID: order.ID,
		Sequence: order.Sequence,
		PlannedArrivalNs: order.PlannedArrivalNs,
		Pickup: GeoPointResponse(order.Pickup),
	}
}

func mapOrders(orders []model.Order) []OrderPreviewResponse{
	ordersResponse := make([]OrderPreviewResponse, 0)

	for _, order := range orders{
		orderResponse := mapOrder(order)
		ordersResponse = append(ordersResponse, orderResponse)
	}

	return ordersResponse
}

func mapAssignments(assigns []model.Assignment) []AssignmentPreviewResponse {
	assignsResponse := make([]AssignmentPreviewResponse, 0)
	
	for _, assign := range assigns{
		assignResponse := AssignmentPreviewResponse{
			OrderID: assign.OrderID,
			Sequence: assign.Sequence,
			RiderUID: assign.RiderUID,
			DistanceMeters: math.Sqrt(assign.DistanceSquaredMeters) ,
		}

		assignsResponse = append(assignsResponse, assignResponse)
	}
	return assignsResponse
}

func mapRunMap(result matchrun.Result) RunMapResponse {
	return RunMapResponse{
		Bounds: mapBounds(result.Bounds),
		Riders: mapRiders(result.RiderPreview),
		Orders: mapOrders(result.Pipeline.OrderPreview),
		Assignments: mapAssignments(result.Pipeline.AssignmentPreview),
	}

}

func mapDeferred(snapShot deferred.MemorySnapshot, previewSize int) DeferredResponse {
	deferredOrdersResponse := make([]DeferredOrderResponse, 0)

	limit := previewSize
	if limit < 0 {
		limit = 0
	}

	if limit > len(snapShot.Orders) {
		limit  = len(snapShot.Orders)
	}


	for i, order := range snapShot.Orders{
		if i >= limit {
			break
		}

		deferredOrderResponse := DeferredOrderResponse{
			Order: mapOrder(order.Order),
			Reason: string(order.Reason),
			DeferredAtNs: order.DeferredAtNs,
			Attempt: order.Attempt,
		}

		deferredOrdersResponse = append(deferredOrdersResponse, deferredOrderResponse)
	}
	
	return DeferredResponse{
		Total: snapShot.Total,
		Orders: deferredOrdersResponse,
	}
}


func mapRoundResponse(
	result matchrun.Result,
	snapshot deferred.MemorySnapshot,
	previewSize int,
	round int,
	inputSource string,
	sourceRound int,
	gracePeriodMs int64,
) RoundResponse {
	deferred := mapDeferred(snapshot, previewSize)

	return RoundResponse{
		Round:             round,
		InputSource:       inputSource,
		SourceRound:       sourceRound,
		InputCount:        result.Pipeline.GeneratedOrders,
		MatchedCount:      result.Pipeline.CompletedOrders,
		DeferredTotal:     result.Pipeline.DeferredOrders,
		DeferredCapacity:  result.Pipeline.DeferredByCapacity,
		DeferredWindow:    result.Pipeline.DeferredByWindow,
		MaxOrdersPerRider: result.Config.MaxOrdersPerRider,
		GracePeriodMs:     gracePeriodMs,
		DeferredSamples:   deferred.Orders,
	}
}
