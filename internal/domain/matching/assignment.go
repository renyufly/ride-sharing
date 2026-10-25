package matching

// Assignment records the rider selected by the matching algorithm for an order.
type Assignment struct {
	OrderID               uint64  // 哪一笔订单
	Sequence              uint64  // 这笔订单在 benchmark 中是第几个到达的
	RiderUID              uint64  // 算法选中的骑手
	DistanceSquaredMeters float64 // 保存“距离平方” 【性能优化，不用开平方】
}
