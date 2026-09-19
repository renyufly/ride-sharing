package resource

import (
	"runtime"
	"testing"
	"time"
)

func TestMonitorSamplesAndStopsIdempotently(t *testing.T) {
	monitor, err := Start(time.Millisecond)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	allocation := make([]byte, 1<<20)
	for index := range allocation {
		allocation[index] = byte(index)
	}
	time.Sleep(3 * time.Millisecond)
	runtime.KeepAlive(allocation)

	first := monitor.Stop()
	second := monitor.Stop()
	if first != second {
		t.Fatalf("Stop() is not idempotent: first=%+v second=%+v", first, second)
	}
	if first.Samples < 2 || first.ElapsedNs <= 0 {
		t.Fatalf("Stats = %+v, want multiple samples and positive elapsed time", first)
	}
	if first.PeakHeapAllocBytes < first.StartHeapAllocBytes || first.PeakRuntimeSysBytes == 0 {
		t.Fatalf("Stats = %+v, invalid peaks", first)
	}
}

func TestStartRejectsInvalidInterval(t *testing.T) {
	if _, err := Start(0); err == nil {
		t.Fatal("Start(0) error = nil")
	}
}
