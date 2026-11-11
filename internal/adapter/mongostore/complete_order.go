package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

func (s *OrderStore) CompleteScheduledOrder(
	ctx context.Context, orderID, riderUID uint64,
) error {
	return s.completeScheduledOrder(ctx, orderID, riderUID, nil)
}

func (s *OrderStore) CompleteScheduledOrderForSession(
	ctx context.Context, route dispatch.RiderSession, orderID uint64,
) error {
	return s.completeScheduledOrder(ctx, orderID, route.RiderUID, &route)
}

func (s *OrderStore) completeScheduledOrder(
	ctx context.Context, orderID, riderUID uint64, route *dispatch.RiderSession,
) error {
	session, err := s.orders.Database().Client().StartSession()
	if err != nil {
		return fmt.Errorf("start complete order session: %w", err)
	}
	defer session.EndSession(ctx)

	id := strconv.FormatUint(orderID, 10)
	riderID := strconv.FormatUint(riderUID, 10)
	txnOptions := options.Transaction().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		result, err := s.orders.UpdateOne(tx, bson.M{
			"_id":                id,
			"status":             dispatch.ScheduleAssigned,
			"assignment.riderId": riderID,
		}, bson.M{
			"$set": bson.M{"status": dispatch.ScheduleCompleted},
			"$push": bson.M{"trace": traceEntry(dispatch.OrderTraceEvent{
				Step:    "completed",
				Pod:     s.podName,
				At:      time.Now().UTC(),
				RiderID: riderID,
			})},
		})
		if err != nil {
			return nil, err
		}

		if result.MatchedCount == 0 {
			var doc orderDocument
			err := s.orders.FindOne(tx, bson.M{"_id": id}).Decode(&doc)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, dispatch.ErrOrderNotAssigned
			}
			if err != nil {
				return nil, err
			}
			if doc.Assignment == nil {
				return nil, dispatch.ErrOrderNotAssigned
			}
			if doc.Assignment.RiderID != riderID {
				return nil, dispatch.ErrOrderRiderMismatch
			}
			if doc.Status == dispatch.ScheduleCompleted {
				return nil, nil
			}
			return nil, dispatch.ErrOrderNotAssigned
		}

		riderFilter := bson.M{"_id": riderID}
		if route != nil {
			riderFilter = riderSessionFilter(*route)
			riderFilter["presenceUntil"] = bson.M{"$gt": time.Now().UTC()}
		}
		riderFilter["active"] = bson.M{"$gt": 0}
		result, err = s.riders.UpdateOne(tx, riderFilter, bson.M{
			"$inc": bson.M{"active": -1},
		})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount == 0 {
			return nil, fmt.Errorf("no matching rider with active capacity to release for rider %s", riderID)
		}
		return nil, nil
	}, txnOptions)
	if err != nil {
		return fmt.Errorf("complete scheduled order %d: %w", orderID, err)
	}
	return nil
}
