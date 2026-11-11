package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *OrderStore) ClaimNext(
	ctx context.Context, leaseDuration time.Duration,
) (*dispatch.ClaimedOrder, error) {
	if leaseDuration < time.Millisecond {
		return nil, errors.New("claim lease must be at least one millisecond")
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	leaseUntil := now.Add(leaseDuration)
	filter := bson.M{
		"status": bson.M{"$in": []dispatch.ScheduleStatus{
			dispatch.ScheduleQueued, dispatch.ScheduleMatching,
		}},
		"nextActionAt": bson.M{"$lte": now},
		"deadlineAt":   bson.M{"$gt": now},
	}
	
	update := bson.M{
		"$set": bson.M{
			"status":       dispatch.ScheduleMatching,
			"claimToken":   uuid.NewString(),
			"leaseUntil":   leaseUntil,
			"nextActionAt": leaseUntil,
		},
		"$push": bson.M{"trace": traceEntry(dispatch.OrderTraceEvent{
			Step: "claimed",
			Pod:  s.podName,
			At:   now,
		})},
	}

	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "nextActionAt", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)

	var doc orderDocument
	err := s.orders.FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim next matcher order: %w", err)
	}

	orderID, err := strconv.ParseUint(doc.ID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse claimed order ID %q: %w", doc.ID, err)
	}

	tried := make(map[uint64]struct{}, len(doc.AttemptedRiderIDs))
	for _, id := range doc.AttemptedRiderIDs {
		riderUID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse attempted rider ID %q: %w", id, err)
		}
		tried[riderUID] = struct{}{}
	}

	return &dispatch.ClaimedOrder{
		OrderID: orderID,
		Spec: dispatch.ScheduleSpec{
			Pickup: geo.GeoPoint{
				Latitude:  doc.Payload.Latitude,
				Longitude: doc.Payload.Longitude,
			},
			TopK:                   doc.Payload.TopK,
			MaxExtraDistanceMeters: doc.Payload.MaxExtraDistanceMeters,
		},
		ClaimToken: doc.ClaimToken,
		LeaseUntil: doc.LeaseUntil,
		DeadlineAt: doc.DeadlineAt,
		TriedRiders: tried,
	}, nil
}

func (s *OrderStore) ExpireBeforeOffer(ctx context.Context) (bool, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	filter := bson.M{
		"status": bson.M{"$in": []dispatch.ScheduleStatus{
			dispatch.ScheduleQueued, dispatch.ScheduleMatching,
		}},
		"deadlineAt": bson.M{"$lte": now},
	}
	update := bson.M{
		"$set": bson.M{"status": dispatch.ScheduleUnassigned},
		"$unset": bson.M{
			"claimToken":   "",
			"leaseUntil":   "",
			"nextActionAt": "",
		},
	}

	result, err := s.orders.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("expire matcher order before offer: %w", err)
	}
	return result.ModifiedCount == 1, nil
}

func (s *OrderStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.orders.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "nextActionAt", Value: 1},
				{Key: "_id", Value: 1},
			},
			Options: options.Index().SetName("order_claim_due"),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "deadlineAt", Value: 1},
			},
			Options: options.Index().SetName("order_deadline"),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "offer.gatewayId", Value: 1},
				{Key: "offer.nextDeliveryAt", Value: 1},
				{Key: "_id", Value: 1},
			},
			Options: options.Index().SetName("order_nextdelivery"),
		},
	})
	
	if err != nil {
		return err
	}

	_, err = s.gateways.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

func (s *OrderStore) RequeueClaim(
	ctx context.Context, claim dispatch.ClaimedOrder, delay time.Duration,
) (bool, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	nextActionAt := now.Add(delay)
	if nextActionAt.After(claim.DeadlineAt) {
		nextActionAt = claim.DeadlineAt
	}

	filter := bson.M{
		"_id":        strconv.FormatUint(claim.OrderID, 10),
		"status":     dispatch.ScheduleMatching,
		"claimToken": claim.ClaimToken,
		"leaseUntil": bson.M{"$gt": now},
		"deadlineAt": bson.M{"$gt": now},
	}
	update := bson.M{
		"$set": bson.M{
			"status":       dispatch.ScheduleQueued,
			"nextActionAt": nextActionAt,
		},
		"$unset": bson.M{
			"claimToken": "",
			"leaseUntil": "",
		},
	}

	result, err := s.orders.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("requeue matcher order %d: %w", claim.OrderID, err)
	}
	return result.ModifiedCount == 1, nil
}