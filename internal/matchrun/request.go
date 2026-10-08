package matchrun

import (
	"ride-sharing/internal/config"
	"ride-sharing/internal/pipeline"
)

type Request struct {
	Config config.Config
	PreviewSize int  // 展示抽样数
	DeferredSink pipeline.DeferredOrderSink  // 保存本轮再次产生的 deferred
	OrderSource pipeline.OrderSource  // 可选；重试时传入 MemorySource
}

