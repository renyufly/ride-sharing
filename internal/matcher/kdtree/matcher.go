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

const missingNode = -1

type node struct {
	rider      model.Rider
	left       int
	right      int
	axis       uint8
	minX, maxX float64
	minY, maxY float64
}

func (m Matcher) IndexStats() matchrule.IndexStats {
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
func New(riders []model.Rider) (Matcher, error) {
	ownedRiders, err := matchrule.CopyAndValidateRiders(riders)
	if err != nil {
		return Matcher{}, err
	}

	matcher := Matcher{
		nodes: make([]node, 0, len(ownedRiders)),
		root:  missingNode,
	}
	matcher.root = matcher.build(ownedRiders, 0)
	return matcher, nil
}

func (m *Matcher) build(riders []model.Rider, depth int) int {
	if len(riders) == 0 {
		return missingNode
	}

	axis := uint8(depth % 2)
	sort.Slice(riders, func(first, second int) bool {
		return riderLessOnAxis(riders[first], riders[second], axis)
	})
	median := len(riders) / 2
	rider := riders[median]
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

	left := m.build(riders[:median], depth+1)
	right := m.build(riders[median+1:], depth+1)
	m.nodes[nodeIndex].left = left
	m.nodes[nodeIndex].right = right
	m.expandBounds(nodeIndex, left)
	m.expandBounds(nodeIndex, right)
	return nodeIndex
}

func (m *Matcher) expandBounds(parentIndex, childIndex int) {
	if childIndex == missingNode {
		return
	}
	parent := &m.nodes[parentIndex]
	child := m.nodes[childIndex]
	parent.minX = min(parent.minX, child.minX)
	parent.maxX = max(parent.maxX, child.maxX)
	parent.minY = min(parent.minY, child.minY)
	parent.maxY = max(parent.maxY, child.maxY)
}

// Match returns the exact nearest rider. Subtrees are pruned only when their
// bounding-box lower bound is strictly greater than the best known distance;
// equal bounds remain searchable so a smaller tied UID cannot be missed.
func (m Matcher) Match(order model.Order) (model.Assignment, error) {
	if m.root == missingNode || len(m.nodes) == 0 {
		return model.Assignment{}, errors.New("matcher has no riders")
	}
	if !matchrule.IsFinitePoint(order.Point) {
		return model.Assignment{}, fmt.Errorf("order %d has a non-finite projected point", order.ID)
	}

	rootRider := m.nodes[m.root].rider
	best := nearest{
		riderUID: rootRider.UID,
		distance: matchrule.DistanceSquared(order.Point, rootRider.Point),
	}
	m.search(m.root, order.Point, &best)

	return model.Assignment{
		OrderID:               order.ID,
		Sequence:              order.Sequence,
		RiderUID:              best.riderUID,
		DistanceSquaredMeters: best.distance,
	}, nil
}

type nearest struct {
	riderUID uint64
	distance float64
}

type childCandidate struct {
	index      int
	lowerBound float64
}

func (m Matcher) search(nodeIndex int, target model.Point2D, best *nearest) {
	current := m.nodes[nodeIndex]
	distance := matchrule.DistanceSquared(target, current.rider.Point)
	if matchrule.IsBetter(distance, current.rider.UID, best.distance, best.riderUID) {
		best.riderUID = current.rider.UID
		best.distance = distance
	}

	first := m.childCandidate(current.left, target)
	second := m.childCandidate(current.right, target)
	if second.index != missingNode && (first.index == missingNode || second.lowerBound < first.lowerBound) {
		first, second = second, first
	}

	if first.index != missingNode && first.lowerBound <= best.distance {
		m.search(first.index, target, best)
	}
	if second.index != missingNode && second.lowerBound <= best.distance {
		m.search(second.index, target, best)
	}
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
