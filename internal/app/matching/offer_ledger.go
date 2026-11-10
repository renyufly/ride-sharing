package matching

import domainmatching "ride-sharing/internal/domain/matching"

// OfferLedger 实时协调器需要的进程共享状态。
type OfferLedger interface {
	Begin(orderID uint64) error
	SnapshotInto(
		riderUIDs []uint64,
		loads []uint64,
		reserved []uint64,
	) (<-chan struct{}, error)
	TryReserve(
		orderID, riderUID uint64,
		maxOrdersPerRider int,
	) (bool, error)
	Release(orderID, riderUID uint64) error
	Commit(assignment domainmatching.Assignment) error
	Abort(orderID uint64)
}