package pipeline

import "testing"

func TestEstimateMemoryUsesRidersWorkersAndBoundedBuffers(t *testing.T) {
	options := Options{Workers: 2, BatchSize: 256, ChannelCapacity: 16, ExpectedOrders: 10_000}
	estimate, err := EstimateMemory(100_000, options)
	if err != nil {
		t.Fatalf("EstimateMemory() error = %v", err)
	}
	if estimate.BufferCount != 18 {
		t.Fatalf("BufferCount = %d, want Q+W = 18", estimate.BufferCount)
	}
	if estimate.WorkerPrivateCountBytes != 1_600_000 {
		t.Fatalf("WorkerPrivateCountBytes = %d, want 1,600,000", estimate.WorkerPrivateCountBytes)
	}
	wantBuffers := uint64(18*256) * estimate.OrderSizeBytes
	if estimate.OrderBufferBytes != wantBuffers {
		t.Fatalf("OrderBufferBytes = %d, want %d", estimate.OrderBufferBytes, wantBuffers)
	}
}

func TestEstimateMemoryDoesNotDependOnTotalOrders(t *testing.T) {
	first, err := EstimateMemory(10_000, Options{Workers: 2, BatchSize: 256, ChannelCapacity: 16, ExpectedOrders: 1})
	if err != nil {
		t.Fatalf("EstimateMemory(first) error = %v", err)
	}
	second, err := EstimateMemory(10_000, Options{Workers: 2, BatchSize: 256, ChannelCapacity: 16, ExpectedOrders: 10_000_000})
	if err != nil {
		t.Fatalf("EstimateMemory(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("memory estimate changed with order total:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestEstimateMemoryRejectsInvalidShape(t *testing.T) {
	if _, err := EstimateMemory(0, Options{Workers: 1, BatchSize: 1, ChannelCapacity: 1}); err == nil {
		t.Fatal("EstimateMemory(zero riders) error = nil")
	}
	if _, err := EstimateMemory(1, Options{}); err == nil {
		t.Fatal("EstimateMemory(zero options) error = nil")
	}
}
