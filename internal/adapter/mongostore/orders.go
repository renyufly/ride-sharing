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

// 订单持久化

// OrderStore 保存指向 MongoDB 集合的handler (不直接保存订单数据)
type OrderStore struct {
	orders *mongo.Collection
	riders *mongo.Collection
}

func NewOrderStore(db *mongo.Database) *OrderStore {
	// Primary 负责接收写入，Secondary 复制数据
	opt := options.Collection().
			SetReadPreference(readpref.Primary()).
			SetReadConcern(readconcern.Majority()).
			SetWriteConcern(writeconcern.Majority())
	return &OrderStore{
		orders: db.Collection("matcher_orders", opt),
		riders: db.Collection("matcher_riders", opt),
	}
}

type orderPayload struct {
	Latitude               float64 `bson:"latitude"`
	Longitude              float64 `bson:"longitude"`
	TopK                   int     `bson:"topK"`
	MaxExtraDistanceMeters float64 `bson:"maxExtraDistanceMeters"`
}

type orderDocument struct {
	ID           string                  `bson:"_id"`
	Payload      orderPayload            `bson:"payload"`
	Status       dispatch.ScheduleStatus `bson:"status"`
	CreatedAt    time.Time               `bson:"createdAt"`
	NextActionAt time.Time               `bson:"nextActionAt"`
	DeadlineAt   time.Time               `bson:"deadlineAt"`
	ClaimToken 	 string                  `bson:"claimToken,omitempty"`
	LeaseUntil   time.Time               `bson:"leaseUntil,omitempty"`
	Offer        *offerDocument          `bson:"offer,omitempty"`
	AttemptedRiderIDs []string           `bson:"attemptedRiderIds,omitempty"`
	Assignment   *assignmentDocument     `bson:"assignment,omitempty"`
}

type offerDocument struct {
	RequestID             string    `bson:"requestId"`
	RiderID               string    `bson:"riderId"`
	GatewayID             string    `bson:"gatewayId"`
	SessionID             string    `bson:"sessionId"`
	ExpiresAt             time.Time `bson:"expiresAt"`
	NextDeliveryAt        time.Time `bson:"nextDeliveryAt"`
	DistanceSquaredMeters float64   `bson:"distanceSquaredMeters"`
}

type assignmentDocument struct {
	RequestID             string  `bson:"requestId"`
	RiderID               string  `bson:"riderId"`
	DistanceSquaredMeters float64 `bson:"distanceSquaredMeters"`
}


func (s *OrderStore) Enqueue(
	ctx context.Context,
	orderID uint64,
	spec dispatch.ScheduleSpec,
	deadlineAt time.Time,
) (dispatch.ScheduleStatus, error) {
	now := time.Now().UTC()
	doc := orderDocument{
		ID: strconv.FormatUint(orderID, 10),
		// 将应用层的 ScheduleSpec 转换为 MongoDB 存储模型
		Payload: orderPayload{
			Latitude:               spec.Pickup.Latitude,
			Longitude:              spec.Pickup.Longitude,
			TopK:                   spec.TopK,
			MaxExtraDistanceMeters: spec.MaxExtraDistanceMeters,
		},
		Status:       dispatch.ScheduleQueued,
		CreatedAt:    now,
		NextActionAt: now,
		DeadlineAt:   deadlineAt.UTC(),
	}

	// 尝试写入 MongoDB
	// 先执行数据库的原子插入操作, 如果重复再FindOne并比较
	_, err := s.orders.InsertOne(ctx, doc)
	if err == nil {
		return doc.Status, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return "", fmt.Errorf("insert matcher order %d: %w", orderID, err)
	}

	var existing orderDocument
	err = s.orders.FindOne(ctx, bson.M{"_id": doc.ID}).Decode(&existing)
	if err != nil {
		return "", fmt.Errorf("read existing matcher order %d: %w", orderID, err)
	}
	if existing.Payload != doc.Payload {
		return "", dispatch.ErrOrderPayloadConflict
	}

	return existing.Status, nil
}

func (s *OrderStore) GetStatus(
	ctx context.Context, orderID uint64,
) (dispatch.ScheduleStatus, error) {
	var doc orderDocument
	filter := bson.M{"_id": strconv.FormatUint(orderID, 10)}
	err := s.orders.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", dispatch.ErrScheduledOrderNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read matcher order %d: %w", orderID, err)
	}
	return doc.Status, nil
}

func (s *OrderStore) ScheduledStatusForRider(
	ctx context.Context, route dispatch.RiderSession, orderID uint64,
) (dispatch.RiderOrderStatus, error) {
	var doc orderDocument
	filter := bson.M{"_id": strconv.FormatUint(orderID, 10)}
	err := s.orders.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return dispatch.RiderOrderUnknown, nil
	}
	if err != nil {
		return "", fmt.Errorf("read rider order %d: %w", orderID, err)
	}

	riderID := strconv.FormatUint(route.RiderUID, 10)
	if doc.Assignment != nil && doc.Assignment.RiderID == riderID {
		switch doc.Status {
		case dispatch.ScheduleAssigned:
			return dispatch.RiderOrderAssigned, nil
		case dispatch.ScheduleCompleted:
			return dispatch.RiderOrderCompleted, nil
		}
	}

	offer := doc.Offer
	if doc.Status == dispatch.ScheduleOffered && offer != nil &&
		offer.RiderID == riderID &&
		offer.GatewayID == route.GatewayID &&
		offer.SessionID == route.SessionID &&
		offer.ExpiresAt.After(time.Now()) {
		
		current, err := s.IsRiderSessionCurrent(ctx, route)
		if err != nil {
			return "", err
		}
		if current {
			return dispatch.RiderOrderPending, nil
		}
	}

	return dispatch.RiderOrderUnknown, nil
}