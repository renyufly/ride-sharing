package dispatch

type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusDeclined Status = "declined"
	StatusTimedOut Status = "timed_out"
	StatusDeliveryFailed Status = "delivery_failed"
)

// 派单尝试
type Attempt struct {
	OrderID   uint64
	RiderUID  uint64
	RequestID string
	Status    Status
}