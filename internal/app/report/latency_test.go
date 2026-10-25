package report

import "testing"

func TestLatencyHistogramSummarizesWithoutRetainingSamples(t *testing.T) {
	values := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	var histogram LatencyHistogram
	for _, value := range values {
		histogram.Observe(value)
	}

	summary := histogram.Summary()
	if summary.Count != uint64(len(values)) {
		t.Fatalf("Count = %d, want %d", summary.Count, len(values))
	}
	if summary.MinNs != 10 || summary.MaxNs != 100 {
		t.Fatalf("min/max = %d/%d, want 10/100", summary.MinNs, summary.MaxNs)
	}
	if summary.P50Ns < 50 || summary.P50Ns > summary.MaxNs {
		t.Fatalf("P50Ns = %d, want conservative bound in [50,100]", summary.P50Ns)
	}
	if summary.P95Ns < 100 || summary.P99Ns < 100 {
		t.Fatalf("P95/P99 = %d/%d, want 100/100", summary.P95Ns, summary.P99Ns)
	}
}

func TestLatencyHistogramMerge(t *testing.T) {
	first := LatencyHistogram{}
	second := LatencyHistogram{}
	first.Observe(-1)
	first.Observe(100)
	second.Observe(50)
	second.Observe(200)
	first.Merge(&second)

	summary := first.Summary()
	if summary.Count != 4 || summary.MinNs != 0 || summary.MaxNs != 200 {
		t.Fatalf("merged summary = %+v", summary)
	}
}

func TestLatencyBucketContainsObservedValue(t *testing.T) {
	values := []uint64{0, 1, 16, 17, 1_000, 1_000_000, 60_000_000_000}
	for _, value := range values {
		index := latencyBucketIndex(value)
		if index < 0 || index >= latencyBucketCount {
			t.Fatalf("bucket index for %d = %d, outside histogram", value, index)
		}
		if upper := latencyBucketUpperBound(index); upper < value {
			t.Fatalf("bucket upper bound for %d = %d", value, upper)
		}
	}
}
