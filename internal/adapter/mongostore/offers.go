package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

var errOfferNotCreated = errors.New("offer conditions no longer match")

func (s *OrderStore) CreateOffer(
	ctx context.Context,
	claim dispatch.ClaimedOrder,
	rider dispatch.RiderSession,
	distanceSquaredMeters float64,
	maxOrdersPerRider int,
	replyTimeout time.Duration,
) (bool, error) {
	if replyTimeout <= 0 {
		return false, errors.New("offer reply timeout must be positive")
	}

	session, err := s.orders.Database().Client().StartSession()
	if err != nil {
		return false, fmt.Errorf("start offer transaction session: %w", err)
	}
	defer session.EndSession(ctx)

	orderID := strconv.FormatUint(claim.OrderID, 10)
	riderID := strconv.FormatUint(rider.RiderUID, 10)
	requestID := uuid.NewString()
	lastAttemptIndex := fmt.Sprintf("attemptedRiderIds.%d", dispatch.MaxScheduleOffers-1)

	txnOptions := options.Transaction().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		now := time.Now().UTC().Truncate(time.Millisecond)
		expiresAt := now.Add(replyTimeout)
		if expiresAt.After(claim.DeadlineAt) {
			expiresAt = claim.DeadlineAt
		}
		if !expiresAt.After(now) {
			return nil, errOfferNotCreated
		}

		live, err := s.gatewayIsLive(tx, rider.GatewayID, now)
		if err != nil {
			return nil, err
		}
		if !live {
			return nil, errOfferNotCreated
		}

		orderFilter := bson.M{
			"_id":               orderID,
			"status":            dispatch.ScheduleMatching,
			"claimToken":        claim.ClaimToken,
			"leaseUntil":        bson.M{"$gt": now},
			"deadlineAt":        bson.M{"$gt": now},
			"attemptedRiderIds": bson.M{"$ne": riderID},
			lastAttemptIndex:    bson.M{"$exists": false},
		}
		orderUpdate := bson.M{
			"$set": bson.M{
				"status":       dispatch.ScheduleOffered,
				"nextActionAt": expiresAt,
				"offer": offerDocument{
					RequestID:             requestID,
					RiderID:               riderID,
					GatewayID:             rider.GatewayID,
					SessionID:             rider.SessionID,
					ExpiresAt:             expiresAt,
					NextDeliveryAt:        now,
					DistanceSquaredMeters: distanceSquaredMeters,
				},
			},
			"$push": bson.M{
				"attemptedRiderIds": riderID,
				"trace": traceEntry(dispatch.OrderTraceEvent{
					Step:      "offer_created",
					Pod:       s.podName,
					At:        now,
					RequestID: requestID,
					RiderID:   riderID,
				}),
			},
			"$unset": bson.M{"claimToken": "", "leaseUntil": ""},
		}
		result, err := s.orders.UpdateOne(tx, orderFilter, orderUpdate)
		if err != nil {
			return nil, err
		}
		if result.MatchedCount == 0 {
			return nil, errOfferNotCreated
		}

		riderFilter := bson.M{
			"_id":           riderID,
			"gatewayId":     rider.GatewayID,
			"sessionId":     rider.SessionID,
			"presenceUntil": bson.M{"$gt": now},
			"locationAt":    bson.M{"$exists": true},
		}
		if maxOrdersPerRider > 0 {
			riderFilter["$expr"] = bson.M{"$lt": bson.A{
				bson.M{"$add": bson.A{"$active", "$reserved"}},
				maxOrdersPerRider,
			}}
		}

		result, err = s.riders.UpdateOne(tx, riderFilter, bson.M{
			"$inc": bson.M{"reserved": 1},
		})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount == 0 {
			return nil, errOfferNotCreated
		}

		return nil, nil
	}, txnOptions)

	if errors.Is(err, errOfferNotCreated) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create offer for order %d: %w", claim.OrderID, err)
	}
	return true, nil
}