// Package matcher contains the exact distance and tie-breaking rules shared by
// all strategy A matcher implementations.
package matcher

import (
	"errors"
	"fmt"
	"math"

	"ride-sharing/internal/model"
)

// 负责的是“统一规则 + 公共算法”

// 索引内存统计 (性能统计)
type IndexStats struct {
	Kind           string `json:"kind"`
	EntryCount     int    `json:"entryCount"`
	EntrySizeBytes uint64 `json:"entrySizeBytes"`
	EstimatedBytes uint64 `json:"estimatedBytes"`
}

// 一个候选骑手，以及这个骑手距离当前订单多远
type Candidate struct {
	RiderUID              uint64  `json:"riderUid"`
	DistanceSquaredMeters float64 `json:"distanceSquaredMeters"`  // 实际上存的是距离平方
}

func CandidateLess(first, second Candidate) bool {
	return IsBetter(
		first.DistanceSquaredMeters,
		first.RiderUID,
		second.DistanceSquaredMeters,
		second.RiderUID,
	)
}

// SortCandidates uses allocation-free insertion sort. Top-K is deliberately
// small, so O(K²) comparisons are cheaper than reflection-backed sort.Slice
// and avoid per-order heap allocations on the hot path.
// 插入排序 (Insertion Sort)
// 不用 sort.Slice-O(K log K)，因为K很小，而且插排实现简单、没有额外内存分配
// Heap只保证：root 是最差的，不保证整个数组是排序好的
func SortCandidates(candidates []Candidate) {
	for index := 1; index < len(candidates); index++ {
		value := candidates[index]
		position := index
		for position > 0 && CandidateLess(value, candidates[position-1]) {
			candidates[position] = candidates[position-1]
			position--
		}
		candidates[position] = value
	}
}

// RetainCandidate maintains a max-heap containing the best limit candidates
// without interface conversions or allocations.
// 扫描大量 Rider 的过程中，始终只保留最好的 K 个 Candidate
// 使用 Max-Heap 【Heap 顶部放的是：当前 Top-K 中最差的那个】
// 按 CandidateLess 定义，最差 Candidate 在堆顶。
// 因为来了一个新 Candidate，只需要比较堆顶，然后选择保留还是替换
func RetainCandidate(candidates []Candidate, limit int, candidate Candidate) []Candidate {
	if len(candidates) < limit {
		candidates = append(candidates, candidate)

		// sift up / 上浮
		index := len(candidates) - 1
		for index > 0 {
			parent := (index - 1) / 2
			// 好的往下、差的往上——Max-Heap
			if !CandidateLess(candidates[parent], candidates[index]) {
				break
			}
			candidates[parent], candidates[index] = candidates[index], candidates[parent]
			index = parent
		}
		return candidates
	}
	if !CandidateLess(candidate, candidates[0]) {
		return candidates
	}

	// 最终 candidates[0] 是 当前 Top-K 里面最差的人
	candidates[0] = candidate

	// sift down / 下沉
	for index := 0; ; {
		left := index*2 + 1
		if left >= len(candidates) {
			break
		}
		worst := left
		right := left + 1
		if right < len(candidates) && CandidateLess(candidates[worst], candidates[right]) {
			worst = right
		}
		if !CandidateLess(candidates[index], candidates[worst]) {
			break
		}
		candidates[index], candidates[worst] = candidates[worst], candidates[index]
		index = worst
	}
	return candidates
}

func CopyAndValidateRiders(riders []model.Rider) ([]model.Rider, error) {
	if len(riders) == 0 {
		return nil, errors.New("at least one rider is required")
	}

	seenUIDs := make(map[uint64]struct{}, len(riders))  // 检测 UID 是否重复 (map[T]struct{} 模拟 Set)
	ownedRiders := make([]model.Rider, len(riders))  // 创建一个全新的 slice
	for index, rider := range riders {
		if !IsFinitePoint(rider.Point) {
			// 验证坐标
			return nil, fmt.Errorf("rider %d has a non-finite projected point", rider.UID)
		}
		if _, exists := seenUIDs[rider.UID]; exists {
			return nil, fmt.Errorf("duplicate rider UID %d", rider.UID)
		}
		seenUIDs[rider.UID] = struct{}{}
		ownedRiders[index] = rider
	}
	return ownedRiders, nil
}

// 二维欧氏距离公式去掉 sqrt
func DistanceSquared(first, second model.Point2D) float64 {
	deltaX := first.X - second.X
	deltaY := first.Y - second.Y
	return deltaX*deltaX + deltaY*deltaY
}

// 比较规则
// 如果候选骑手距离更近，那么 candidate 更好；
// 如果距离完全相同，则 UID 更小的更好
func IsBetter(candidateDistance float64, candidateUID uint64, bestDistance float64, bestUID uint64) bool {
	return candidateDistance < bestDistance ||
		(candidateDistance == bestDistance && candidateUID < bestUID)
}

func IsFinitePoint(point model.Point2D) bool {
	return !math.IsNaN(point.X) && !math.IsInf(point.X, 0) &&
		!math.IsNaN(point.Y) && !math.IsInf(point.Y, 0)
}
