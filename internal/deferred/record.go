package deferred

import (
	"ride-sharing/internal/model"
	"ride-sharing/internal/pipeline"
)

type deferredRecord struct {
	Version int   `json:"version"`
	Attempt uint32 `json:"attempt"`
	Reason  pipeline.DeferredReason `json:"reason"`
	DeferredAtNs int64  `json:"deferredAtNs"`
	Order   orderRecord `json:"order"`
}

type orderRecord struct {
	ID	uint64	`json:"id"`
	Sequence	uint64	`json:"sequence"`
	PlannedArrivalNs	int64	`json:"plannedArrivalNs"`
	Pickup	geoPointRecord	`json:"pickup"`
	Point	pointRecord	`json:"point"`
}

type geoPointRecord struct {
	Latitude	float64	`json:"latitude"`
Longitude	float64	`json:"longitude"`
}

type pointRecord struct {
	X	float64	`json:"xMeters"`
	Y	float64	`json:"yMeters"`
}

func newDeferredRecord(order pipeline.DeferredOrder) deferredRecord {
	return deferredRecord{
		Version: 1,
		Attempt: order.Attempt,
		Reason: order.Reason,
		DeferredAtNs: order.DeferredAtNs,
		Order: orderRecord{
			ID: order.Order.ID,
			Sequence: order.Order.Sequence,
			PlannedArrivalNs: order.Order.PlannedArrivalNs,
			Pickup: geoPointRecord{
				Latitude: order.Order.Pickup.Latitude,
				Longitude: order.Order.Pickup.Longitude,
			},
			Point: pointRecord{
				X: order.Order.Point.X,
				Y: order.Order.Point.Y,
			},
		},
	}

}

// 转换回model.Order
func toModelOrder(record deferredRecord, sequence uint64) model.Order {
	return model.Order{
		ID: record.Order.ID,
		Sequence: sequence,
		PlannedArrivalNs: 0,  // 在第二轮立即可处理
		Pickup: model.GeoPoint(record.Order.Pickup),
		Point: model.Point2D(record.Order.Point),
	}


}