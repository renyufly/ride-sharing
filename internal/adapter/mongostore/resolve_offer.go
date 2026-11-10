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

var errOfferUnchanged = errors.New("offer no longer eligible for resolution")

type offerReply struct {
	route    dispatch.RiderSession
	accepted bool
}

func (s *OrderStore) RespondToOffer(
	ctx context.Context,
	route dispatch.RiderSession,
	orderID uint64,
	requestID string,
	accepted bool,
) (bool, error) {
	return s.resolveOffer(
		ctx, strconv.FormatUint(orderID, 10), requestID,
		&offerReply{route: route, accepted: accepted},
	)
}

func (s *OrderStore) ExpireNextOffer(ctx context.Context) (bool, error) {
	filter := bson.M{
		"status":       dispatch.ScheduleOffered,
		"nextActionAt": bson.M{"$lte": time.Now().UTC()},
	}
	opts := options.FindOne().SetSort(bson.D{
		{Key: "nextActionAt", Value: 1},
		{Key: "_id", Value: 1},
	})

	var doc orderDocument
	err := s.orders.FindOne(ctx, filter, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find expired offer: %w", err)
	}
	if doc.Offer == nil {
		return false, fmt.Errorf("offered order %s has no offer", doc.ID)
	}

	return s.resolveOffer(ctx, doc.ID, doc.Offer.RequestID, nil)
}

func (s *OrderStore) resolveOffer(
	ctx context.Context, orderID, requestID string, reply *offerReply,
) (bool, error) {
	session, err := s.orders.Database().Client().StartSession()
	if err != nil {
		return false, fmt.Errorf("start offer resolution session: %w", err)
	}
	defer session.EndSession(ctx)

	txnOptions := options.Transaction().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		return nil, s.resolveOfferInTransaction(tx, orderID, requestID, reply)
	}, txnOptions)
	if errors.Is(err, errOfferUnchanged) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve offer for order %s: %w", orderID, err)
	}
	return true, nil
}

func (s *OrderStore) resolveOfferInTransaction(
	tx mongo.SessionContext, orderID, requestID string, reply *offerReply,
) error {
	var doc orderDocument
	err := s.orders.FindOne(tx, bson.M{"_id": orderID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errOfferUnchanged
	}
	if err != nil {
		return err
	}

	if reply != nil && reply.accepted &&
		doc.Status == dispatch.ScheduleAssigned &&
		doc.Assignment != nil &&
		doc.Assignment.RequestID == requestID &&
		doc.Assignment.RiderID == strconv.FormatUint(reply.route.RiderUID, 10) {
		return nil
	}

	if doc.Status != dispatch.ScheduleOffered || doc.Offer == nil ||
		doc.Offer.RequestID != requestID {
		return errOfferUnchanged
	}

	now := time.Now().UTC()
	orderFilter := bson.M{
		"_id":             orderID,
		"status":          dispatch.ScheduleOffered,
		"offer.requestId": requestID,
	}
	riderFilter := bson.M{"_id": doc.Offer.RiderID}
	if reply == nil {
		orderFilter["offer.expiresAt"] = bson.M{"$lte": now}
	} else {
		riderFilter = riderSessionFilter(reply.route)
		riderFilter["presenceUntil"] = bson.M{"$gt": now}
		orderFilter["offer.expiresAt"] = bson.M{"$gt": now}
		orderFilter["offer.riderId"] = strconv.FormatUint(reply.route.RiderUID, 10)
		orderFilter["offer.gatewayId"] = reply.route.GatewayID
		orderFilter["offer.sessionId"] = reply.route.SessionID
	}
	riderFilter["reserved"] = bson.M{"$gt": 0}

	set := bson.M{"status": dispatch.ScheduleUnassigned}
	unset := bson.M{"offer": "", "nextActionAt": ""}
	capacityChange := bson.M{"reserved": -1}

	if reply != nil && reply.accepted {
		set["status"] = dispatch.ScheduleAssigned
		set["assignment"] = assignmentDocument{
			RequestID:             requestID,
			RiderID:               doc.Offer.RiderID,
			DistanceSquaredMeters: doc.Offer.DistanceSquaredMeters,
		}
		capacityChange["active"] = 1
	} else if len(doc.AttemptedRiderIDs) < dispatch.MaxScheduleOffers &&
		doc.DeadlineAt.After(now) {
		set["status"] = dispatch.ScheduleQueued
		set["nextActionAt"] = now
		delete(unset, "nextActionAt")
	}

	result, err := s.orders.UpdateOne(tx, orderFilter, bson.M{
		"$set": set, "$unset": unset,
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errOfferUnchanged
	}

	result, err = s.riders.UpdateOne(tx, riderFilter, bson.M{"$inc": capacityChange})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		if reply != nil {
			return errOfferUnchanged
		}
		return fmt.Errorf("rider %s has no reservation for expired offer %s",
			doc.Offer.RiderID, requestID)
	}
	return nil
}