package pipeline

import "math/bits"

const (
	latencySubBuckets  = 16
	latencyBucketCount = 1 + 63*latencySubBuckets
)

// LatencySummary is a fixed-memory percentile summary. Percentiles are the
// conservative upper bounds of logarithmic histogram buckets; min and max are
// exact observations.
type LatencySummary struct {
	Count uint64 `json:"count"`
	MinNs int64  `json:"minNs"`
	P50Ns int64  `json:"p50Ns"`
	P95Ns int64  `json:"p95Ns"`
	P99Ns int64  `json:"p99Ns"`
	MaxNs int64  `json:"maxNs"`
}

type latencyHistogram struct {
	counts [latencyBucketCount]uint64
	count  uint64
	minNs  int64
	maxNs  int64
}

func (h *latencyHistogram) observe(valueNs int64) {
	if valueNs < 0 {
		valueNs = 0
	}
	if h.count == 0 || valueNs < h.minNs {
		h.minNs = valueNs
	}
	if h.count == 0 || valueNs > h.maxNs {
		h.maxNs = valueNs
	}
	h.counts[latencyBucketIndex(uint64(valueNs))]++
	h.count++
}

func (h *latencyHistogram) merge(other *latencyHistogram) {
	if other == nil || other.count == 0 {
		return
	}
	if h.count == 0 || other.minNs < h.minNs {
		h.minNs = other.minNs
	}
	if h.count == 0 || other.maxNs > h.maxNs {
		h.maxNs = other.maxNs
	}
	for index, count := range other.counts {
		h.counts[index] += count
	}
	h.count += other.count
}

func (h *latencyHistogram) summary() LatencySummary {
	if h.count == 0 {
		return LatencySummary{}
	}
	return LatencySummary{
		Count: h.count,
		MinNs: h.minNs,
		P50Ns: h.percentile(50),
		P95Ns: h.percentile(95),
		P99Ns: h.percentile(99),
		MaxNs: h.maxNs,
	}
}

func (h *latencyHistogram) percentile(percent uint64) int64 {
	target := (h.count*percent + 99) / 100
	var cumulative uint64
	for index, count := range h.counts {
		cumulative += count
		if cumulative >= target {
			upper := latencyBucketUpperBound(index)
			if upper > uint64(h.maxNs) {
				return h.maxNs
			}
			return int64(upper)
		}
	}
	return h.maxNs
}

func latencyBucketIndex(value uint64) int {
	if value <= latencySubBuckets {
		return int(value)
	}
	exponent := bits.Len64(value) - 1
	base := uint64(1) << exponent
	width := base / latencySubBuckets
	slot := int((value - base) / width)
	if slot >= latencySubBuckets {
		slot = latencySubBuckets - 1
	}
	return 1 + exponent*latencySubBuckets + slot
}

func latencyBucketUpperBound(index int) uint64 {
	if index <= latencySubBuckets {
		return uint64(index)
	}
	position := index - 1
	exponent := position / latencySubBuckets
	slot := position % latencySubBuckets
	base := uint64(1) << exponent
	width := base / latencySubBuckets
	return base + uint64(slot+1)*width - 1
}
