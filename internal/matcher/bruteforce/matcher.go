// Package bruteforce implements the serial, exact nearest-rider baseline.
package bruteforce

import (
	"errors"
	"fmt"
	"unsafe"

	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/model"
)

// 整个骑手匹配系统里的“标准答案算法”
// 每来一个订单，就把所有骑手从头到尾检查一遍，计算订单到每个骑手的距离，最后选择最近的骑手
// serial：串行扫描
// exact：精确结果
// nearest-rider：最近骑手
// baseline：基准算法，后面的 KD-Tree 等优化算法可以拿它来做正确性和性能对比
// 总复杂度：O(N × M)
type Matcher struct {
	riders []model.Rider  // 一个 Matcher 内部保存所有骑手
}
// Brute Force 没有建立任何空间索引


// 找最近的 K 个骑手
func (m Matcher) TopK(order model.Order, limit int) ([]matchrule.Candidate, error) {
	return m.TopKInto(order, limit, nil)
}

// destination：允许调用者传一个已经存在的 slice (复用内存，减少 GC 压力)
func (m Matcher) TopKInto(order model.Order, limit int, destination []matchrule.Candidate) ([]matchrule.Candidate, error) {
	if len(m.riders) == 0 {
		return nil, errors.New("matcher has no riders")
	}
	if limit <= 0 {
		return nil, errors.New("top-k limit must be greater than zero")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		return nil, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}
	if limit > len(m.riders) {
		limit = len(m.riders)
	}
	if cap(destination) < limit {
		destination = make([]matchrule.Candidate, 0, limit)
	} else {
		destination = destination[:0]  // 长度清零，但继续使用原来的内存 (典型的 Go 性能优化技巧)
	}
	candidates := destination

	// Brute-force：把所有骑手扫描一遍
	for _, rider := range m.riders {
		candidate := matchrule.Candidate{
			RiderUID:              rider.UID,
			DistanceSquaredMeters: matchrule.DistanceSquared(order.Point, rider.Point),
		}

		// candidates 最多只保留最好的 K 个骑手
		candidates = matchrule.RetainCandidate(candidates, limit, candidate)
	}
	result := candidates
	matchrule.SortCandidates(result) // 最后统一排序
	return result, nil
}

// 统计这个 Matcher 的索引占用了多少内存
func (m Matcher) IndexStats() matchrule.IndexStats {
	entrySize := uint64(unsafe.Sizeof(model.Rider{}))  // 一个 Rider 结构体占多少字节
	return matchrule.IndexStats{
		Kind:           "rider-slice",
		EntryCount:     len(m.riders),
		EntrySizeBytes: entrySize,
		EstimatedBytes: uint64(len(m.riders)) * entrySize,
	}
}

// New validates and copies the static rider set. Copying prevents a caller
// from changing coordinates while a run is in progress.
// 创建 Matcher
func New(riders []model.Rider) (Matcher, error) {
	// 构造时复制骑手集合，Matcher 获得自己的静态快照 (浅复制, 因为没有 map、slice 或指针成员)
	ownedRiders, err := matchrule.CopyAndValidateRiders(riders)
	if err != nil {
		return Matcher{}, err
	}
	return Matcher{riders: ownedRiders}, nil
}

// Match scans every rider and returns the exact nearest rider in the projected
// coordinate system. Exact distance ties are resolved by the smaller UID.
// 核心：给我一个订单，我返回离这个订单最近的骑手
func (m Matcher) Match(order model.Order) (model.Assignment, error) {
	if len(m.riders) == 0 {
		// 首先检查有没有骑手
		return model.Assignment{}, errors.New("matcher has no riders")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		// 检查订单坐标是否合法 (投影后的二维坐标)
		return model.Assignment{}, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}

	// brute-force
	bestRider := m.riders[0]  // 先假设第一个骑手最好
	bestDistance := matchrule.DistanceSquared(order.Point, bestRider.Point)
	for index := 1; index < len(m.riders); index++ {
		// 从第二个骑手开始扫描
		candidate := m.riders[index]

		// 计算候选骑手距离
		candidateDistance := matchrule.DistanceSquared(order.Point, candidate.Point)
		// 如果两个骑手距离完全相同，UID 小的获胜
		if matchrule.IsBetter(candidateDistance, candidate.UID, bestDistance, bestRider.UID) {
			bestRider = candidate
			bestDistance = candidateDistance
		}
	}

	// 最后构造 Assignment
	return model.Assignment{
		OrderID:               order.ID,
		Sequence:              order.Sequence,
		RiderUID:              bestRider.UID,
		DistanceSquaredMeters: bestDistance,
	}, nil
}
