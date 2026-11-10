package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/order"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *OrderStore) ClaimNextDelivery(
	ctx context.Context, gatewayID string,
) (*dispatch.OfferDelivery, error) {
	now := time.Now().UTC()
	filter := bson.M{
		"status":               dispatch.ScheduleOffered,
		"offer.gatewayId":      gatewayID,
		"offer.nextDeliveryAt": bson.M{"$lte": now},
		"offer.expiresAt":      bson.M{"$gt": now},
	}
	update := bson.M{
		"$set": bson.M{"offer.nextDeliveryAt": now.Add(2 * time.Second)},
	}
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "offer.nextDeliveryAt", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)

	var doc orderDocument
	err := s.orders.FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim offer delivery: %w", err)
	}
	if doc.Offer == nil {
		return nil, fmt.Errorf("order %s has no offer to deliver", doc.ID)
	}

	orderID, err := strconv.ParseUint(doc.ID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse delivery order ID %q: %w", doc.ID, err)
	}
	riderUID, err := strconv.ParseUint(doc.Offer.RiderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse delivery rider ID %q: %w", doc.Offer.RiderID, err)
	}

	return &dispatch.OfferDelivery{
		Route: dispatch.RiderSession{
			RiderUID:  riderUID,
			GatewayID: doc.Offer.GatewayID,
			SessionID: doc.Offer.SessionID,
		},
		Offer: dispatch.Offer{
			RequestID: doc.Offer.RequestID,
			Order: order.Order{
				ID: orderID,
				Pickup: geo.GeoPoint{
					Latitude:  doc.Payload.Latitude,
					Longitude: doc.Payload.Longitude,
				},
			},
		},
		ExpiresAt: doc.Offer.ExpiresAt,
	}, nil
}

func (s *OrderStore) StopOfferDelivery(
	ctx context.Context, delivery dispatch.OfferDelivery,
) error {
	_, err := s.orders.UpdateOne(ctx, bson.M{
		"_id":             strconv.FormatUint(delivery.Offer.Order.ID, 10),
		"status":          dispatch.ScheduleOffered,
		"offer.requestId": delivery.Offer.RequestID,
		"offer.sessionId": delivery.Route.SessionID,
	}, bson.M{
		"$unset": bson.M{"offer.nextDeliveryAt": ""},
	})
	return err
}