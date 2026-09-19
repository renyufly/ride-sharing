// Package resource samples Go runtime memory and GC metrics for one matcher
// run. It reports Go-managed memory, not operating-system RSS.
package resource

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

type Stats struct {
	SampleInterval       string `json:"sampleInterval"`
	ElapsedNs            int64  `json:"elapsedNs"`
	Samples              uint64 `json:"samples"`
	StartHeapAllocBytes  uint64 `json:"startHeapAllocBytes"`
	EndHeapAllocBytes    uint64 `json:"endHeapAllocBytes"`
	PeakHeapAllocBytes   uint64 `json:"peakHeapAllocBytes"`
	PeakHeapInuseBytes   uint64 `json:"peakHeapInuseBytes"`
	PeakRuntimeSysBytes  uint64 `json:"peakRuntimeSysBytes"`
	TotalAllocDeltaBytes uint64 `json:"totalAllocDeltaBytes"`
	MallocsDelta         uint64 `json:"mallocsDelta"`
	FreesDelta           uint64 `json:"freesDelta"`
	NumGCDelta           uint32 `json:"numGCDelta"`
	GCPauseDeltaNs       uint64 `json:"gcPauseDeltaNs"`
	PeakGoroutines       int    `json:"peakGoroutines"`
}

type Monitor struct {
	interval time.Duration
	started  time.Time
	start    runtime.MemStats
	stop     chan struct{}
	done     sync.WaitGroup
	stopOnce sync.Once
	mu       sync.Mutex
	stats    Stats
}

func Start(interval time.Duration) (*Monitor, error) {
	if interval <= 0 {
		return nil, errors.New("resource sample interval must be greater than zero")
	}
	monitor := &Monitor{
		interval: interval,
		started:  time.Now(),
		stop:     make(chan struct{}),
	}
	runtime.ReadMemStats(&monitor.start)
	monitor.done.Add(1)
	go monitor.sample()
	return monitor, nil
}

func (m *Monitor) Stop() Stats {
	m.stopOnce.Do(func() { close(m.stop) })
	m.done.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *Monitor) sample() {
	defer m.done.Done()
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	peakHeapAlloc := m.start.HeapAlloc
	peakHeapInuse := m.start.HeapInuse
	peakSys := m.start.Sys
	peakGoroutines := runtime.NumGoroutine()
	var samples uint64 = 1
	var end runtime.MemStats

	read := func() {
		runtime.ReadMemStats(&end)
		samples++
		peakHeapAlloc = max(peakHeapAlloc, end.HeapAlloc)
		peakHeapInuse = max(peakHeapInuse, end.HeapInuse)
		peakSys = max(peakSys, end.Sys)
		peakGoroutines = max(peakGoroutines, runtime.NumGoroutine())
	}

	for {
		select {
		case <-ticker.C:
			read()
		case <-m.stop:
			read()
			m.mu.Lock()
			m.stats = Stats{
				SampleInterval:       m.interval.String(),
				ElapsedNs:            time.Since(m.started).Nanoseconds(),
				Samples:              samples,
				StartHeapAllocBytes:  m.start.HeapAlloc,
				EndHeapAllocBytes:    end.HeapAlloc,
				PeakHeapAllocBytes:   peakHeapAlloc,
				PeakHeapInuseBytes:   peakHeapInuse,
				PeakRuntimeSysBytes:  peakSys,
				TotalAllocDeltaBytes: delta(end.TotalAlloc, m.start.TotalAlloc),
				MallocsDelta:         delta(end.Mallocs, m.start.Mallocs),
				FreesDelta:           delta(end.Frees, m.start.Frees),
				NumGCDelta:           uint32(delta(uint64(end.NumGC), uint64(m.start.NumGC))),
				GCPauseDeltaNs:       delta(end.PauseTotalNs, m.start.PauseTotalNs),
				PeakGoroutines:       peakGoroutines,
			}
			m.mu.Unlock()
			return
		}
	}
}

func delta(end, start uint64) uint64 {
	if end < start {
		return 0
	}
	return end - start
}
