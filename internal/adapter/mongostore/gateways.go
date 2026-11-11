package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *OrderStore) RenewGateway(
	ctx context.Context, gatewayID string, lease time.Duration,
) error {
	_, err := s.gateways.UpdateOne(
		ctx,
		bson.M{"_id": gatewayID},
		bson.M{"$max": bson.M{"expiresAt": time.Now().UTC().Add(lease)}},
		options.Update().SetUpsert(true),
	)
	return err
}

func (s *OrderStore) RemoveGateway(ctx context.Context, gatewayID string) error {
	_, err := s.gateways.DeleteOne(ctx, bson.M{"_id": gatewayID})
	return err
}

func (s *OrderStore) liveGatewayIDs(ctx context.Context, now time.Time) (bson.A, error) {
	ids, err := s.gateways.Distinct(ctx, "_id", bson.M{
		"expiresAt": bson.M{"$gt": now},
	})
	if err != nil {
		return nil, fmt.Errorf("list live gateways: %w", err)
	}
	if len(ids) == 0 {
		return bson.A{}, nil
	}
	return bson.A(ids), nil
}

func (s *OrderStore) gatewayIsLive(
	ctx context.Context, gatewayID string, now time.Time,
) (bool, error) {
	err := s.gateways.FindOne(ctx, bson.M{
		"_id":       gatewayID,
		"expiresAt": bson.M{"$gt": now},
	}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check gateway %s lease: %w", gatewayID, err)
	}
	return true, nil
}