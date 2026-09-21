// Package kdtree implements an exact nearest-rider search over a static,
// balanced two-dimensional KD-tree.
package kdtree

import (
	"errors"
	"fmt"
	"sort"
	"unsafe"

	matchrule "ride-sharing/internal/matcher"
	"ride-sharing/internal/model"
)

// brute-force matcher 的高性能替代实现
// KD-Tree：先把静态骑手位置组织成一棵空间搜索树，订单来了以后，
// 利用“某些区域肯定不可能更近”这一事实，直接剪掉大量骑手
// 一次剪枝跳过整棵子树，而不是让单次距离计算更快

// 还做了三个重要的工程优化：
// 平衡 KD-Tree：尽量避免树退化。
// 每个子树保存 Bounding Box：增强剪枝能力。
// 同时支持 最近 1 个骑手 Match() 和 最近 K 个骑手 TopK()

// KD-Tree：启动时 build 一次，之后不修改
// 适合 多 goroutine 并发订单匹配

const missingNode = -1  // 如果left=-1 就没有左孩子

// KD-Tree 的核心节点：KD-Tree 的核心节点
type node struct {
	rider      model.Rider  // 当前骑手
	left       int   // 左子树，存的是子节点在 nodes slice 中的下标 (比大量独立 *node 对象更紧凑，也更利于内存局部性)
	right      int
	axis       uint8     // 当前节点按 X 还是 Y 划分
	minX, maxX float64
	minY, maxY float64
}

// 统计 KD-Tree 内存
// 它只是估算 nodes 本身，不一定等于整个 Go 进程实际内存
func (m Matcher) IndexStats() matchrule.IndexStats {
	// 获取一个 node 在内存中占多少字节
	entrySize := uint64(unsafe.Sizeof(node{}))

	return matchrule.IndexStats{
		Kind:           "kd-tree-node-slice",
		EntryCount:     len(m.nodes),
		EntrySizeBytes: entrySize,
		EstimatedBytes: uint64(len(m.nodes)) * entrySize,
	}
}

type Matcher struct {
	nodes []node
	root  int
}

// New copies the rider set and builds a median-balanced tree. The tree is
// immutable after construction and can later be shared by concurrent readers.
// 开始建 KD-Tree
func New(riders []model.Rider) (Matcher, error) {
	// 下面建树的时候会修改 slice 中骑手的排列顺序
	ownedRiders, err := matchrule.CopyAndValidateRiders(riders)
	if err != nil {
		return Matcher{}, err
	}

	matcher := Matcher{
		nodes: make([]node, 0, len(ownedRiders)),
		root:  missingNode,
	}
	matcher.root = matcher.build(ownedRiders, 0) // depth = 0 代表从树根开始
	return matcher, nil
}

// 建树
func (m *Matcher) build(riders []model.Rider, depth int) int {
	if len(riders) == 0 {
		// 递归终止条件
		return missingNode
	}

	// axis = 0 → 按 X 分  axis = 1 → 按 Y 分
	// 不断：X → Y → X → Y 所以叫：k-dimensional tree
	axis := uint8(depth % 2)

	// 排序
	sort.Slice(riders, func(first, second int) bool {
		return riderLessOnAxis(riders[first], riders[second], axis)
	})

	// 中位数：让左右子树大小尽量接近，构造 balanced tree
	median := len(riders) / 2
	rider := riders[median]

	// 创建当前 node
	nodeIndex := len(m.nodes)
	m.nodes = append(m.nodes, node{
		rider: rider,
		left:  missingNode,
		right: missingNode,
		axis:  axis,
		minX:  rider.Point.X,
		maxX:  rider.Point.X,
		minY:  rider.Point.Y,
		maxY:  rider.Point.Y,
	})

	// 递归建立左右子树
	left := m.build(riders[:median], depth+1)
	right := m.build(riders[median+1:], depth+1)

	// 把左右子树记录下来
	m.nodes[nodeIndex].left = left
	m.nodes[nodeIndex].right = right

	// expandBounds()是优化：让每个node还知道 以自己为根的整个子树覆盖了哪一块空间
	m.expandBounds(nodeIndex, left)
	m.expandBounds(nodeIndex, right)
	return nodeIndex
}

func (m *Matcher) expandBounds(parentIndex, childIndex int) {
	if childIndex == missingNode {
		return
	}
	parent := &m.nodes[parentIndex]  // 用指针，因为要直接修改父节点
	child := m.nodes[childIndex]

	parent.minX = min(parent.minX, child.minX)
	parent.maxX = max(parent.maxX, child.maxX)
	parent.minY = min(parent.minY, child.minY)
	parent.maxY = max(parent.maxY, child.maxY)
}

// Match returns the exact nearest rider. Subtrees are pruned only when their
// bounding-box lower bound is strictly greater than the best known distance;
// equal bounds remain searchable so a smaller tied UID cannot be missed.
// 真正匹配订单：真正匹配订单
func (m Matcher) Match(order model.Order) (model.Assignment, error) {
	if m.root == missingNode || len(m.nodes) == 0 {
		return model.Assignment{}, errors.New("matcher has no riders")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		return model.Assignment{}, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}

	// 先拿 root 当作“目前最优答案”
	rootRider := m.nodes[m.root].rider
	best := nearest{
		riderUID: rootRider.UID,
		distance: matchrule.DistanceSquared(order.Point, rootRider.Point),
	}

	// 核心搜索 (递归过程中会不断更新：当前最近骑手)
	m.search(m.root, order.Point, &best)

	return model.Assignment{
		OrderID:               order.ID,
		Sequence:              order.Sequence,
		RiderUID:              best.riderUID,
		DistanceSquaredMeters: best.distance,
	}, nil
}

// TopK returns the exact K nearest riders ordered by distance and UID. It is
// additive to Match so strategy A keeps its frozen single-nearest path.
func (m Matcher) TopK(order model.Order, limit int) ([]matchrule.Candidate, error) {
	return m.TopKInto(order, limit, nil)
}

// 性能优化：允许调用者提供一个已经分配好的 slice
// 避免： 每个订单 -> make slice -> heap allocation -> GC 压力
func (m Matcher) TopKInto(order model.Order, limit int, destination []matchrule.Candidate) ([]matchrule.Candidate, error) {
	if m.root == missingNode || len(m.nodes) == 0 {
		return nil, errors.New("matcher has no riders")
	}
	if limit <= 0 {
		return nil, errors.New("top-k limit must be greater than zero")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		return nil, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}
	if limit > len(m.nodes) {
		limit = len(m.nodes)
	}

	if cap(destination) < limit {
		destination = make([]matchrule.Candidate, 0, limit)
	} else {
		destination = destination[:0]
	}
	candidates := destination
	m.searchTopK(m.root, order.Point, limit, &candidates)
	result := candidates
	matchrule.SortCandidates(result)
	return result, nil
}

type nearest struct {
	riderUID uint64
	distance float64
}

type childCandidate struct {
	index      int   // 子树根节点
	lowerBound float64  // 订单到这个子树 Bounding Box 的最短可能距离² (距离的理论下界)
}

func (m Matcher) search(nodeIndex int, target model.Point2D, best *nearest) {
	current := m.nodes[nodeIndex]  // 检查当前 rider

	// 计算：订单 ↔ 当前骑手 距离
	distance := matchrule.DistanceSquared(target, current.rider.Point)
	if matchrule.IsBetter(distance, current.rider.UID, best.distance, best.riderUID) {
		best.riderUID = current.rider.UID
		best.distance = distance
	}

	// 计算子树最小可能距离
	first := m.childCandidate(current.left, target)
	second := m.childCandidate(current.right, target)

	// 先搜索 lowerBound 更小的子树
	if second.index != missingNode && (first.index == missingNode || second.lowerBound < first.lowerBound) {
		first, second = second, first
	}

	// 最关键的剪枝判断
	if first.index != missingNode && first.lowerBound <= best.distance {
		m.search(first.index, target, best)
	}
	if second.index != missingNode && second.lowerBound <= best.distance {
		m.search(second.index, target, best)
	}
}

func (m Matcher) searchTopK(nodeIndex int, target model.Point2D, limit int, candidates *[]matchrule.Candidate) {
	current := m.nodes[nodeIndex]
	candidate := matchrule.Candidate{
		RiderUID:              current.rider.UID,
		DistanceSquaredMeters: matchrule.DistanceSquared(target, current.rider.Point),
	}

	// 维护当前最好的 K 个 rider
	*candidates = matchrule.RetainCandidate(*candidates, limit, candidate)

	first := m.childCandidate(current.left, target)
	second := m.childCandidate(current.right, target)
	if second.index != missingNode && (first.index == missingNode || second.lowerBound < first.lowerBound) {
		first, second = second, first
	}

	if m.topKChildCanImprove(first, limit, candidates) {
		m.searchTopK(first.index, target, limit, candidates)
	}
	if m.topKChildCanImprove(second, limit, candidates) {
		m.searchTopK(second.index, target, limit, candidates)
	}
}

// TopK的剪枝
func (m Matcher) topKChildCanImprove(child childCandidate, limit int, candidates *[]matchrule.Candidate) bool {
	// candidates[0]是当前TopK里最差(距离最远)的那个候选人
	return child.index != missingNode &&
		(len(*candidates) < limit || child.lowerBound <= (*candidates)[0].DistanceSquaredMeters)
}

func (m Matcher) childCandidate(nodeIndex int, target model.Point2D) childCandidate {
	if nodeIndex == missingNode {
		return childCandidate{index: missingNode}
	}
	return childCandidate{
		index:      nodeIndex,
		lowerBound: distanceToBoundsSquared(target, m.nodes[nodeIndex]),
	}
}

func distanceToBoundsSquared(point model.Point2D, bounds node) float64 {
	
	// 计算点到矩形距离
	deltaX := 0.0
	if point.X < bounds.minX {
		deltaX = bounds.minX - point.X
	} else if point.X > bounds.maxX {
		deltaX = point.X - bounds.maxX
	}
	deltaY := 0.0
	if point.Y < bounds.minY {
		deltaY = bounds.minY - point.Y
	} else if point.Y > bounds.maxY {
		deltaY = point.Y - bounds.maxY
	}
	return deltaX*deltaX + deltaY*deltaY
}

// 即使骑手坐标相同，建出来的树依然具有确定性
func riderLessOnAxis(first, second model.Rider, axis uint8) bool {
	firstPrimary, secondPrimary := first.Point.X, second.Point.X
	firstSecondary, secondSecondary := first.Point.Y, second.Point.Y
	if axis == 1 {
		firstPrimary, secondPrimary = first.Point.Y, second.Point.Y
		firstSecondary, secondSecondary = first.Point.X, second.Point.X
	}
	if firstPrimary != secondPrimary {
		return firstPrimary < secondPrimary
	}
	if firstSecondary != secondSecondary {
		return firstSecondary < secondSecondary
	}
	return first.UID < second.UID
}
