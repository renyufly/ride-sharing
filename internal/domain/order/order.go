package order

import "ride-sharing/internal/domain/geo"

type Order struct {
	ID               uint64       // 业务订单 ID
	Sequence         uint64       // 本轮测试中的到达顺序
	PlannedArrivalNs int64        // 订单计划到达时间(纳秒) (模拟器计划让这个订单在什么时候进入系统)
	Pickup           geo.GeoPoint // 订单的取货位置/上车点
	Point            geo.Point2D
}
