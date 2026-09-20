// Package model defines values shared by generators, matchers, pipelines, and
// reports. It intentionally contains no matching behavior.
package model

// 骑手—订单匹配系统 定义数据模型（Model）. 不包含任何匹配逻辑
type GeoPoint struct {  // 经纬度坐标
	Latitude  float64   // 纬度
	Longitude float64
}

// 提前把某个局部区域转换成二维平面，可以直接计算欧式距离
type Point2D struct {   // 平面坐标（米）
	X float64 // meters
	Y float64 // meters
}

type Rider struct {  // 骑手
	UID      uint64   // 骑手唯一 ID
	Location GeoPoint  // 真实经纬度
	Point    Point2D   // 匹配算法计算使用的二维坐标
}

// Order 把“业务订单 ID”和“本次 benchmark 中连续的订单序号”分开保存
// 并发处理 ≠ 按进入顺序完成 ；方便知道原始顺序
// used to preserve arrival order inside one benchmark run.
type Order struct {
	ID               uint64  // 业务订单 ID
	Sequence         uint64  // 本轮测试中的到达顺序
	PlannedArrivalNs int64   // 订单计划到达时间(纳秒) (模拟器计划让这个订单在什么时候进入系统)
	Pickup           GeoPoint  // 订单的取货位置/上车点
	Point            Point2D
}

// 匹配结果: 某个订单最终匹配给了哪个骑手
type Assignment struct {  
	OrderID               uint64    // 哪一笔订单
	Sequence              uint64    // 这笔订单在 benchmark 中是第几个到达的
	RiderUID              uint64    // 最终分配给哪个骑手
	DistanceSquaredMeters float64   // 保存“距离平方” 【性能优化，不用开平方】
}
