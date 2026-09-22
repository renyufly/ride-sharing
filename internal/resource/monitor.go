// Package resource samples Go runtime memory and GC metrics for one matcher
// run. It reports Go-managed memory, not operating-system RSS.
package resource

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

// 资源监控器：在 2 核 4GB 的资源限制下，这个程序到底消耗了多少资源 (旁路监控组件)
// 匹配程序运行期间，定期给 Go Runtime 拍“资源快照”，
// 最后统计这次匹配任务用了多少堆内存、发生多少次内存分配、
// 触发多少次 GC、GC 暂停多久，以及最多出现多少个 goroutine

// 一次测试运行结束以后，资源监控器输出的最终统计结果
type Stats struct {
	SampleInterval       string `json:"sampleInterval"`   // 采样间隔
	ElapsedNs            int64  `json:"elapsedNs"`
	Samples              uint64 `json:"samples"`    // 采样次数
	StartHeapAllocBytes  uint64 `json:"startHeapAllocBytes"`
	EndHeapAllocBytes    uint64 `json:"endHeapAllocBytes"`
	PeakHeapAllocBytes   uint64 `json:"peakHeapAllocBytes"`  // 运行过程中 Go 堆中“当前存活对象”最高占用了多少空间
	PeakHeapInuseBytes   uint64 `json:"peakHeapInuseBytes"`  // Go Runtime 已经划给 Heap 使用的内存区域
	PeakRuntimeSysBytes  uint64 `json:"peakRuntimeSysBytes"`
	TotalAllocDeltaBytes uint64 `json:"totalAllocDeltaBytes"`  // 整个运行过程中累计申请了多少堆内存 (申请后可以释放)
	MallocsDelta         uint64 `json:"mallocsDelta"`
	FreesDelta           uint64 `json:"freesDelta"`
	NumGCDelta           uint32 `json:"numGCDelta"`  // 一共触发多少次 GC
	GCPauseDeltaNs       uint64 `json:"gcPauseDeltaNs"`
	PeakGoroutines       int    `json:"peakGoroutines"`  // 整个运行期间观测到的最大 goroutine 数量
}

type Monitor struct {
	interval time.Duration
	started  time.Time
	start    runtime.MemStats  // 程序开始监控时的 Runtime 快照
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
