package dispatch

import (
	"errors"
	"time"

	"ride-sharing/internal/domain/geo"
)

type ScheduleStatus string

const (
	ScheduleQueued     ScheduleStatus = "queued"  // 订单已进入调度队列
	ScheduleMatching   ScheduleStatus = "matching"  // 正在寻找合适骑手
	ScheduleUnassigned ScheduleStatus = "unassigned"  // 暂时无法分配骑手
	ScheduleOffered ScheduleStatus = "offered"   // 	已向骑手发送接单邀请
	ScheduleAssigned ScheduleStatus = "assigned"  // 骑手已接单，分配成功
	ScheduleCompleted ScheduleStatus = "completed"  // 订单已完成
)

// 一个订单最多可以发起骑手接单邀请的次数
const MaxScheduleOffers = 5

var ErrOrderPayloadConflict = errors.New("order ID already used with different parameters")
var ErrScheduledOrderNotFound = errors.New("scheduled order not found")

// ScheduleSpec 订单匹配要求
type ScheduleSpec struct {
	Pickup                 geo.GeoPoint
	TopK                   int
	MaxExtraDistanceMeters float64
}

// ClaimedOrder 已领取的调度任务
type ClaimedOrder struct {
	OrderID    uint64
	Spec       ScheduleSpec
	ClaimToken string   // 本次领取任务的唯一标识，用于验证操作权限
	LeaseUntil time.Time  // Worker 对任务的租约到期时间
	DeadlineAt time.Time   // 	订单调度截止时间
	TriedRiders map[uint64]struct{}
}

// RiderSession 骑手连接会话
type RiderSession struct {
	RiderUID  uint64
	GatewayID string
	SessionID string
}

type OnlineRider struct {
	RiderSession
	Location geo.GeoPoint
	Active   uint64
	Reserved uint64
}

type OfferDelivery struct {
	Route     RiderSession
	Offer     Offer
	ExpiresAt time.Time
}