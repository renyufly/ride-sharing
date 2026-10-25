package matchrun

import (
	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/config"
)

type Request struct {
	Config       config.Config
	PreviewSize  int                           // 展示抽样数
	DeferredSink appmatching.DeferredOrderSink // 保存本轮再次产生的 deferred
	OrderSource  appmatching.OrderSource       // 可选；重试时传入 MemorySource
}
