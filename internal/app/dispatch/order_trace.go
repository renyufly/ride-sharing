package dispatch

import "time"

type OrderTraceEvent struct {
	Step      string    `bson:"step" json:"step"`
	Pod       string    `bson:"pod" json:"pod"`
	At        time.Time `bson:"at" json:"at"`
	RequestID string    `bson:"requestId,omitempty" json:"requestId,omitempty"`
	RiderID   string    `bson:"riderId,omitempty" json:"riderId,omitempty"`
}

type ScheduledOrderDetails struct {
	OrderID     string            `json:"orderId"`
	Status      ScheduleStatus    `json:"status"`
	ReceivedPod string            `json:"receivedPod,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	Trace       []OrderTraceEvent `json:"trace,omitempty"`
}