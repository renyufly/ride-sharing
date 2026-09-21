// Package report accumulates assignment metrics without retaining individual
// assignments and produces the deterministic Bottom 10 rider report.
package report

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"

	"ride-sharing/internal/model"
)

// 统计与验收模块
// 所有订单匹配结束以后，分配得怎么样？每个骑手拿了多少单？
// 最少的 10 个是谁？匹配距离怎么样？负载是否均衡？

const (
	bottomLimit                = 10   // 输出分配订单最少的后 10 名骑手
	distanceHistogramMaxMeters = 50_000  // 距离直方图统计到 50KM
)

// 估算 每个 worker 私有统计数据大约占多少内存
type WorkerMemoryEstimate struct {
	RiderCountBytes        uint64 `json:"riderCountBytes"`
	DistanceHistogramBytes uint64 `json:"distanceHistogramBytes"`
}

// EstimateWorkerMemory reports the two dominant worker-private allocations.
// It intentionally excludes slice headers and the shared immutable UID index.
func EstimateWorkerMemory(riderCount int) WorkerMemoryEstimate {
	return WorkerMemoryEstimate{
		RiderCountBytes:        uint64(riderCount) * uint64(8),
		DistanceHistogramBytes: uint64(distanceHistogramMaxMeters+1) * uint64(8),
	}
}

// 骑手 ID + 他一共分到了多少订单
type RiderOrderCount struct {
	RiderUID   uint64 `json:"riderUid"`
	OrderCount uint64 `json:"orderCount"`
}

// 最终成绩单
type Summary struct {
	AssignmentCount           uint64            `json:"assignmentCount"`
	RiderOrderCountSum        uint64            `json:"riderOrderCountSum"`
	Bottom10                  []RiderOrderCount `json:"bottom10"`
	ZeroRiderCount            int               `json:"zeroRiderCount"`
	MinOrders                 uint64            `json:"minOrders"`
	MeanOrders                float64           `json:"meanOrders"`
	MaxOrders                 uint64            `json:"maxOrders"`
	VarianceOrders            float64           `json:"varianceOrders"`
	StdDevOrders              float64           `json:"stdDevOrders"`
	CoefficientOfVariation    float64           `json:"coefficientOfVariation"`
	AverageDistanceMeters     float64           `json:"averageDistanceMeters"`
	P95DistanceMeters         float64           `json:"p95DistanceMeters"`
	MaxDistanceMeters         float64           `json:"maxDistanceMeters"`
	DistanceHistogramOverflow uint64            `json:"distanceHistogramOverflow"`
}

// 统计累加器
type Accumulator struct {
	riderIndex                map[uint64]int
	riderUIDs                 []uint64
	riderCounts               []uint64
	assignmentCount           uint64
	distanceSumMicrometers    uint64
	maxDistanceMeters         float64
	distanceHistogram         []uint64
	distanceHistogramOverflow uint64
}

func New(riders []model.Rider) (*Accumulator, error) {
	if len(riders) == 0 {
		return nil, errors.New("at least one rider is required for reporting")
	}

	accumulator := &Accumulator{
		riderIndex:        make(map[uint64]int, len(riders)),
		riderUIDs:         make([]uint64, len(riders)),
		riderCounts:       make([]uint64, len(riders)),
		distanceHistogram: make([]uint64, distanceHistogramMaxMeters+1),
	}
	for index, rider := range riders {
		if _, exists := accumulator.riderIndex[rider.UID]; exists {
			return nil, fmt.Errorf("duplicate rider UID %d", rider.UID)
		}
		accumulator.riderIndex[rider.UID] = index
		accumulator.riderUIDs[index] = rider.UID
	}
	return accumulator, nil
}

// Fork creates an empty worker-owned accumulator that shares only the
// immutable UID-to-index map. Counts and distance metrics remain private.
// 并发：每一个 worker 有自己的统计器
// 线程/协程本地统计 + 最终归并
func (a *Accumulator) Fork() *Accumulator {
	return &Accumulator{
		riderIndex:        a.riderIndex,  // 只读数据，可安全共享
		riderUIDs:         a.riderUIDs,
		riderCounts:       make([]uint64, len(a.riderCounts)),  // 每个worker要创建自己的local
		distanceHistogram: make([]uint64, len(a.distanceHistogram)),
	}
}

// Observe records one final assignment. It performs the only square root in
// the matching/reporting path, after the winning rider has been selected.
// 每完成一单匹配就调用一次，开始统计
func (a *Accumulator) Observe(assignment model.Assignment) error {
	index, exists := a.riderIndex[assignment.RiderUID]
	if !exists {
		return fmt.Errorf("assignment references unknown rider UID %d", assignment.RiderUID)
	}
	if math.IsNaN(assignment.DistanceSquaredMeters) || math.IsInf(assignment.DistanceSquaredMeters, 0) || assignment.DistanceSquaredMeters < 0 {
		return fmt.Errorf("assignment for order %d has invalid squared distance %v", assignment.OrderID, assignment.DistanceSquaredMeters)
	}

	distanceMeters := math.Sqrt(assignment.DistanceSquaredMeters)

	// 使用整数进行确定性的累计，避免大量浮点加法因执行/归并顺序不同产生微小差异
	distanceMicrometers := math.Round(distanceMeters * 1_000_000)
	if distanceMicrometers > float64(^uint64(0)-a.distanceSumMicrometers) {
		return fmt.Errorf("assignment distance sum overflows uint64 micrometers")
	}
	a.riderCounts[index]++
	a.assignmentCount++
	a.distanceSumMicrometers += uint64(distanceMicrometers)
	if distanceMeters > a.maxDistanceMeters {
		a.maxDistanceMeters = distanceMeters
	}

	bucket := int(math.Ceil(distanceMeters))
	if bucket > distanceHistogramMaxMeters {
		a.distanceHistogramOverflow++
	} else {
		a.distanceHistogram[bucket]++
	}
	return nil
}

// Merge combines a worker-owned accumulator into the receiver. Both
// accumulators must have been created from the same ordered rider set. The
// caller must not mutate source concurrently and should treat it as handed off
// after this call.
// 把多个 worker 的结果合起来
func (a *Accumulator) Merge(source *Accumulator) error {
	if source == nil {
		return errors.New("cannot merge a nil accumulator")
	}
	if len(a.riderUIDs) != len(source.riderUIDs) || len(a.riderCounts) != len(source.riderCounts) || len(a.distanceHistogram) != len(source.distanceHistogram) {
		return errors.New("cannot merge accumulators with different shapes")
	}
	if source.distanceSumMicrometers > ^uint64(0)-a.distanceSumMicrometers {
		return errors.New("cannot merge distance sums: uint64 micrometers overflow")
	}
	for index := range a.riderUIDs {
		if a.riderUIDs[index] != source.riderUIDs[index] {
			return fmt.Errorf("cannot merge rider index %d: UID %d != %d", index, a.riderUIDs[index], source.riderUIDs[index])
		}
	}
	for index := range a.riderCounts {
		a.riderCounts[index] += source.riderCounts[index]
	}
	for index, count := range source.distanceHistogram {
		a.distanceHistogram[index] += count
	}
	a.assignmentCount += source.assignmentCount
	a.distanceSumMicrometers += source.distanceSumMicrometers
	a.distanceHistogramOverflow += source.distanceHistogramOverflow
	if source.maxDistanceMeters > a.maxDistanceMeters {
		a.maxDistanceMeters = source.maxDistanceMeters
	}
	return nil
}

// 生成最终报告
func (a *Accumulator) Summary() Summary {
	// 均值
	mean := float64(a.assignmentCount) / float64(len(a.riderCounts))
	minOrders := a.riderCounts[0]
	var maxOrders uint64
	var countSum uint64
	var squaredDeviationSum float64
	zeroRiders := 0
	riderCounts := make([]RiderOrderCount, len(a.riderCounts))

	// 遍历所有骑手
	for index, count := range a.riderCounts {
		riderCounts[index] = RiderOrderCount{RiderUID: a.riderUIDs[index], OrderCount: count}
		countSum += count // 订单总数

		if count == 0 {
			// 有多少骑手完全没有订单
			zeroRiders++
		}
		if count < minOrders {
			minOrders = count  // 最小订单数
		}
		if count > maxOrders {
			maxOrders = count
		}
		difference := float64(count) - mean
		squaredDeviationSum += difference * difference
	}

	// 方差越大，订单分配越不均衡
	variance := squaredDeviationSum / float64(len(a.riderCounts))
	stdDev := math.Sqrt(variance)
	coefficientOfVariation := 0.0
	if mean != 0 {
		// 变异系数（CV）：标准差相对于平均值有多大
		coefficientOfVariation = stdDev / mean
	}
	averageDistance := 0.0
	if a.assignmentCount != 0 {
		// 平均匹配距离
		averageDistance = float64(a.distanceSumMicrometers) / 1_000_000 / float64(a.assignmentCount)
	}

	return Summary{
		AssignmentCount:           a.assignmentCount,
		RiderOrderCountSum:        countSum,
		Bottom10:                  bottomRiders(riderCounts, bottomLimit),
		ZeroRiderCount:            zeroRiders,
		MinOrders:                 minOrders,
		MeanOrders:                mean,
		MaxOrders:                 maxOrders,
		VarianceOrders:            variance,
		StdDevOrders:              stdDev,
		CoefficientOfVariation:    coefficientOfVariation,
		AverageDistanceMeters:     averageDistance,
		P95DistanceMeters:         a.percentile95(),
		MaxDistanceMeters:         a.maxDistanceMeters,
		DistanceHistogramOverflow: a.distanceHistogramOverflow,
	}
}

// P95：统计约 95% 的订单，其匹配骑手距离不超过x m
// 使用的是 ceil(distance) 的 1 米桶，所以 P95 是一个按米离散后的近似分位数
func (a *Accumulator) percentile95() float64 {
	if a.assignmentCount == 0 {
		return 0
	}
	targetRank := uint64(math.Ceil(float64(a.assignmentCount) * 0.95))
	var cumulative uint64

	// 从最近的距离桶开始累计
	for bucket, count := range a.distanceHistogram {
		cumulative += count
		if cumulative >= targetRank {
			return float64(bucket)
		}
	}
	// If P95 falls into the overflow bucket, return the exact observed maximum
	// as a conservative bound and expose the overflow count in the summary.
	return a.maxDistanceMeters
}

// 寻找分配订单最少的后 10 名骑手
// 如果是所有骑手排序，是O(N log N)
// 这里用 container/heap 维护一个最多只有：10 个元素的heap
// O(N log10) == O(N)
// 找最小 K 个 → 维护大小为 K 的max-heap
func bottomRiders(riders []RiderOrderCount, limit int) []RiderOrderCount {
	if limit > len(riders) {
		limit = len(riders)
	}
	candidates := make(maxRiderHeap, 0, limit)
	for _, rider := range riders {
		if len(candidates) < limit {
			heap.Push(&candidates, rider)
			continue
		}
		if riderLess(rider, candidates[0]) {
			candidates[0] = rider
			heap.Fix(&candidates, 0)
		}
	}

	result := append([]RiderOrderCount(nil), candidates...)
	sort.Slice(result, func(first, second int) bool {
		return riderLess(result[first], result[second])
	})
	return result
}

func riderLess(first, second RiderOrderCount) bool {
	// 订单少的优先
	if first.OrderCount != second.OrderCount {
		return first.OrderCount < second.OrderCount
	}
	// UID 小的优先
	return first.RiderUID < second.RiderUID
}

// maxRiderHeap keeps the worst currently retained Bottom 10 candidate at the
// root: higher order count first, then higher UID.
// 使用max-heap
// 因为默认heap的顶是这 10 个里面最差的那个，也就是订单数最大的那个
// 所以 找最小 K 个 → 维护大小为 K 的最大堆
type maxRiderHeap []RiderOrderCount

func (h maxRiderHeap) Len() int { return len(h) }

func (h maxRiderHeap) Less(first, second int) bool {
	return riderLess(h[second], h[first])
}

func (h maxRiderHeap) Swap(first, second int) {
	h[first], h[second] = h[second], h[first]
}

func (h *maxRiderHeap) Push(value any) {
	*h = append(*h, value.(RiderOrderCount))
}

func (h *maxRiderHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
