package pipeline

import (
	"errors"
	"unsafe"

	"ride-sharing/internal/model"
	"ride-sharing/internal/report"
)

// MemoryEstimate describes allocations whose size is fixed by riders,
// workers, batch size, and queue capacity. It is a structural lower bound, not
// a replacement for measuring process RSS or Go runtime peaks.
type MemoryEstimate struct {
	RiderCount                   int    `json:"riderCount"`
	WorkerCount                  int    `json:"workerCount"`
	BufferCount                  int    `json:"bufferCount"`
	OrderSizeBytes               uint64 `json:"orderSizeBytes"`
	RiderSizeBytes               uint64 `json:"riderSizeBytes"`
	RiderSliceBytes              uint64 `json:"riderSliceBytes"`
	WorkerPrivateCountBytes      uint64 `json:"workerPrivateCountBytes"`
	WorkerDistanceHistogramBytes uint64 `json:"workerDistanceHistogramBytes"`
	LatencyHistogramBytes        uint64 `json:"latencyHistogramBytes"`
	BatchEnvelopeBytes           uint64 `json:"batchEnvelopeBytes"`
	OrderBufferBytes             uint64 `json:"orderBufferBytes"`
	CoreLowerBoundBytes          uint64 `json:"coreLowerBoundBytes"`
}

func EstimateMemory(riderCount int, options Options) (MemoryEstimate, error) {
	if riderCount <= 0 {
		return MemoryEstimate{}, errors.New("rider count must be greater than zero")
	}
	if options.Workers <= 0 || options.BatchSize <= 0 || options.ChannelCapacity <= 0 {
		return MemoryEstimate{}, errors.New("workers, batch size, and channel capacity must be greater than zero")
	}

	orderSize := uint64(unsafe.Sizeof(model.Order{}))
	riderSize := uint64(unsafe.Sizeof(model.Rider{}))
	bufferCount := options.ChannelCapacity + options.Workers
	workerMemory := report.EstimateWorkerMemory(riderCount)

	riderSliceBytes, ok := multiply(uint64(riderCount), riderSize)
	if !ok {
		return MemoryEstimate{}, errors.New("rider slice byte estimate overflow")
	}
	workerCountBytes, ok := multiply(uint64(options.Workers), workerMemory.RiderCountBytes)
	if !ok {
		return MemoryEstimate{}, errors.New("worker count byte estimate overflow")
	}
	workerHistogramBytes, ok := multiply(uint64(options.Workers), workerMemory.DistanceHistogramBytes)
	if !ok {
		return MemoryEstimate{}, errors.New("worker histogram byte estimate overflow")
	}
	bufferOrders, ok := multiply(uint64(bufferCount), uint64(options.BatchSize))
	if !ok {
		return MemoryEstimate{}, errors.New("buffer order estimate overflow")
	}
	orderBufferBytes, ok := multiply(bufferOrders, orderSize)
	if !ok {
		return MemoryEstimate{}, errors.New("order buffer byte estimate overflow")
	}
	latencyHistogramBytes, ok := multiply(uint64(1+2*options.Workers), uint64(unsafe.Sizeof(latencyHistogram{})))
	if !ok {
		return MemoryEstimate{}, errors.New("latency histogram byte estimate overflow")
	}
	batchEnvelopeBytes, ok := multiply(uint64(bufferCount), uint64(unsafe.Sizeof(admittedBatch{})))
	if !ok {
		return MemoryEstimate{}, errors.New("batch envelope byte estimate overflow")
	}
	coreBytes, ok := add(riderSliceBytes, workerCountBytes, workerHistogramBytes, latencyHistogramBytes, batchEnvelopeBytes, orderBufferBytes)
	if !ok {
		return MemoryEstimate{}, errors.New("core byte estimate overflow")
	}

	return MemoryEstimate{
		RiderCount:                   riderCount,
		WorkerCount:                  options.Workers,
		BufferCount:                  bufferCount,
		OrderSizeBytes:               orderSize,
		RiderSizeBytes:               riderSize,
		RiderSliceBytes:              riderSliceBytes,
		WorkerPrivateCountBytes:      workerCountBytes,
		WorkerDistanceHistogramBytes: workerHistogramBytes,
		LatencyHistogramBytes:        latencyHistogramBytes,
		BatchEnvelopeBytes:           batchEnvelopeBytes,
		OrderBufferBytes:             orderBufferBytes,
		CoreLowerBoundBytes:          coreBytes,
	}, nil
}

func multiply(first, second uint64) (uint64, bool) {
	if first != 0 && second > ^uint64(0)/first {
		return 0, false
	}
	return first * second, true
}

func add(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		if value > ^uint64(0)-total {
			return 0, false
		}
		total += value
	}
	return total, true
}
