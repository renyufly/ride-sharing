package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type riderDocument struct {
	ID        string  `bson:"_id"`
	GatewayID string  `bson:"gatewayId"`
	SessionID string  `bson:"sessionId"`
	Latitude  float64 `bson:"latitude"`
	Longitude float64 `bson:"longitude"`
	Active    uint64  `bson:"active"`
	Reserved  uint64  `bson:"reserved"`
}

// RegisterRiderSession 骑手连接时，在 MongoDB 中创建或更新会话
func (s *OrderStore) RegisterRiderSession(
	ctx context.Context, route dispatch.RiderSession, validUntil time.Time,
) error {
	filter := bson.M{"_id": strconv.FormatUint(route.RiderUID, 10)}
	update := bson.M{
		"$set": bson.M{
			"gatewayId":     route.GatewayID,
			"sessionId":     route.SessionID,
			"presenceUntil": validUntil.UTC(),
		},
		"$setOnInsert": bson.M{"active": 0, "reserved": 0},
		"$unset": bson.M{
			"latitude":   "",
			"longitude":  "",
			"locationAt": "",
		},
	}

	_, err := s.riders.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		// 两个连接并发首次注册时，另一方可能已插入同一骑手。
		_, err = s.riders.UpdateOne(ctx, filter, update)
	}
	if err != nil {
		return fmt.Errorf("register rider %d: %w", route.RiderUID, err)
	}
	return nil
}

// RenewRiderSession 延长骑手的在线有效期
func (s *OrderStore) RenewRiderSession(
	ctx context.Context, route dispatch.RiderSession, validUntil time.Time,
) (bool, error) {
	result, err := s.riders.UpdateOne(ctx, riderSessionFilter(route), bson.M{
		"$max": bson.M{"presenceUntil": validUntil.UTC()},
	})
	if err != nil {
		return false, fmt.Errorf("renew rider %d session: %w", route.RiderUID, err)
	}
	return result.MatchedCount == 1, nil
}

// SaveRiderLocation 更新当前在线骑手的经纬度
func (s *OrderStore) SaveRiderLocation(
	ctx context.Context, route dispatch.RiderSession, location geo.GeoPoint,
) (bool, error) {
	if err := geo.ValidatePoint(location); err != nil {
		return false, err
	}

	now := time.Now().UTC()
	filter := riderSessionFilter(route)
	filter["presenceUntil"] = bson.M{"$gt": now}

	result, err := s.riders.UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{
			"latitude":   location.Latitude,
			"longitude":  location.Longitude,
			"locationAt": now,
		},
	})
	if err != nil {
		return false, fmt.Errorf("save rider %d location: %w", route.RiderUID, err)
	}
	return result.MatchedCount == 1, nil
}

// UnregisterRiderSession 骑手断开连接时，清除在线状态
func (s *OrderStore) UnregisterRiderSession(
	ctx context.Context, route dispatch.RiderSession,
) error {
	_, err := s.riders.UpdateOne(ctx, riderSessionFilter(route), bson.M{
		"$unset": bson.M{
			"gatewayId":     "",
			"sessionId":     "",
			"presenceUntil": "",
			"latitude":      "",
			"longitude":     "",
			"locationAt":    "",
		},
	})
	if err != nil {
		return fmt.Errorf("unregister rider %d: %w", route.RiderUID, err)
	}
	return nil
}

func riderSessionFilter(route dispatch.RiderSession) bson.M {
	return bson.M{
		"_id":       strconv.FormatUint(route.RiderUID, 10),
		"gatewayId": route.GatewayID,
		"sessionId": route.SessionID,
	}
}

// ListOnlineRiders 从 MongoDB 查询所有在线骑手
func (s *OrderStore) ListOnlineRiders(ctx context.Context) ([]dispatch.OnlineRider, error) {
	now := time.Now().UTC()
	gatewayIDs, err := s.liveGatewayIDs(ctx, now)
	if err != nil || len(gatewayIDs) == 0 {
		return nil, err
	}

	cursor, err := s.riders.Find(ctx, bson.M{
		"gatewayId":     bson.M{"$in": gatewayIDs},
		"presenceUntil": bson.M{"$gt": now},
		"locationAt":    bson.M{"$exists": true},
	})
	if err != nil {
		return nil, fmt.Errorf("list online riders: %w", err)
	}

	var docs []riderDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode online riders: %w", err)
	}

	riders := make([]dispatch.OnlineRider, 0, len(docs))
	for _, doc := range docs {
		uid, err := strconv.ParseUint(doc.ID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rider ID %q: %w", doc.ID, err)
		}
		riders = append(riders, dispatch.OnlineRider{
			RiderSession: dispatch.RiderSession{
				RiderUID:  uid,
				GatewayID: doc.GatewayID,
				SessionID: doc.SessionID,
			},
			Location: geo.GeoPoint{Latitude: doc.Latitude, Longitude: doc.Longitude},
			Active:   doc.Active,
			Reserved: doc.Reserved,
		})
	}
	return riders, nil
}

func (s *OrderStore) IsRiderSessionCurrent(
	ctx context.Context, route dispatch.RiderSession,
) (bool, error) {
	now := time.Now().UTC()
	live, err := s.gatewayIsLive(ctx, route.GatewayID, now)
	if err != nil || !live {
		return false, err
	}

	filter := riderSessionFilter(route)
	filter["presenceUntil"] = bson.M{"$gt": now}
	err = s.riders.FindOne(ctx, filter).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check rider %d session: %w", route.RiderUID, err)
	}
	return true, nil
}