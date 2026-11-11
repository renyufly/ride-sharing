package mongostore

import (
	"context"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"

	"go.mongodb.org/mongo-driver/bson"
)

// traceEntry 每单保留最近 64 条事件
func traceEntry(event dispatch.OrderTraceEvent) bson.M {
	return bson.M{"$each": bson.A{event}, "$slice": -64}
}

func (s *OrderStore) RecordOfferSent(
	ctx context.Context, delivery dispatch.OfferDelivery,
) error {
	requestID := delivery.Offer.RequestID
	filter := bson.M{
		"_id": strconv.FormatUint(delivery.Offer.Order.ID, 10),
		"trace": bson.M{"$not": bson.M{"$elemMatch": bson.M{
			"step":      "offer_sent",
			"requestId": requestID,
		}}},
	}

	event := dispatch.OrderTraceEvent{
		Step:      "offer_sent",
		Pod:       s.podName,
		At:        time.Now().UTC(),
		RequestID: requestID,
		RiderID:   strconv.FormatUint(delivery.Route.RiderUID, 10),
	}
	_, err := s.orders.UpdateOne(ctx, filter, bson.M{
		"$push": bson.M{"trace": traceEntry(event)},
	})
	return err
}