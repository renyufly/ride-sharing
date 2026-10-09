import type { MatcherRunRequest } from "@/components/matcher/contracts";

export const DEFAULT_REQUEST: MatcherRunRequest = {
  riderCount: 100,
  orderCount: 10000,
  arrivalWindow: "30s",
  arrivalModel: "unbounded",
  seed: 42,
  riderDistribution: "uniform",
  orderDistribution: "uniform",
  algorithm: "kd-tree",
  strategy: "nearest",
  workers: 2,
  batchSize: 1,
  channelCapacity: 16,
  topK: 0,
  maxExtraDistanceMeters: 0,
  maxOrdersPerRider: 0,
  previewSize: 100,
};

export const METRIC_TOOLTIPS = {
  // 延迟统计通用字段
  min: "Min（最小值）：所有有效样本中观测到的最短耗时。",
  p50: "P50（中位数）：约 50% 的样本耗时不超过此值，反映典型响应水平。",
  p95: "P95（第95百分位）：约 95% 的样本耗时不超过此值，反映较慢请求的延迟。",
  p99: "P99（第99百分位）：约 99% 的样本耗时不超过此值，常用于分析长尾延迟。",
  max: "Max（最大值）：所有有效样本中观测到的最长耗时。",
  count:
    "Count（样本数）：参与该延迟统计的有效样本数量，不一定等于生成订单总数。",

  // 四组延迟
  endToEnd:
    "端到端延迟：从订单原计划到达时间开始，到成功完成骑手分配为止。它包含计划到达后的注入延迟、Channel 排队、Worker 处理以及最终匹配时间；只统计成功完成分配的订单。",
  queueAndMatch:
    "排队与匹配延迟：从订单所在 Batch 成功写入处理 Channel，到订单成功完成骑手分配。Nearest 包含 Channel 等待和最近邻匹配；Balanced 还包含候选搜索、乱序等待和 Coordinator 顺序决策。",
  admissionDelay:
    "准入延迟：订单的 Batch 实际进入处理 Channel的时间，减去该订单原计划到达时间。它会受到定时注入、Batch 凑批和 Channel 背压影响。",
  algorithmCompute:
    "Balanced 纯算法耗时：每笔订单的 Top-K 候选搜索耗时，加上 Coordinator 从候选中选择最终骑手的决策耗时。不包含 Channel 排队和等待前序订单的时间。因容量不足而 deferred 的订单也可能计入，因分配窗口过期而 deferred 的订单只会记录候选搜索。",
  nearestSearch:
    "最近邻搜索延迟：Nearest Worker 调用匹配器查找最近骑手所花的时间，不包含 Channel 排队、报告统计和其他 Pipeline 开销。",

  // 流水线
  indexBuild: "索引构建：创建空间索引结构（如 KD-Tree）所花费的时间。",
  riderGeneration: "骑手生成：按照骑手数量和分布参数生成模拟骑手数据的耗时。",
  pipeline:
    "管线执行：从调用订单处理 Pipeline 开始，到 Producer、全部 Worker 和 Coordinator 退出并完成结果汇总。它是本次 Pipeline 调用的实际墙钟耗时。",
  admissionRate:
    "实际准入速率：成功进入处理 Channel 的订单数，除以从 Pipeline 启动到最后一次准入的时间。它反映 Producer、Batch 和背压共同作用后的真实输入速度。",
  completionRate:
    "实际完成速率：成功完成骑手分配的订单数，除以从 Pipeline 启动到最后一次成功分配的时间。Deferred 订单不计入完成数量。",
  actualInjection:
    "实际注入时间：从 Pipeline 启动，到最后一个 Batch 成功写入处理 Channel的时间点。",
  injectionOverrun:
    "注入超时：最后一次实际准入时间减去最后一笔订单的计划到达时间；没有超出时记为 0。它能反映 Batch、调度或 Channel 背压造成的注入拖延。",
  drainAfterWindow:
    "窗口后排空：最后一次成功分配时间减去最后一笔订单的计划到达时间；如果匹配在此之前完成则记为 0。",
  totalRun:
    "最后成功完成时间：从 Pipeline 启动，到最后一笔成功分配订单完成的时间。它不一定等于整个 Pipeline 的墙钟运行时间；完整 Pipeline 耗时请看“管线执行”",

  // 公平性
  averageDistance:
    "平均订单距离：成功完成分配的订单与最终骑手之间的平均直线距离。越小通常代表更接近严格就近分配。",
  p95Distance:
    "P95 订单距离：成功完成分配的订单中，约 95% 的匹配距离不超过此值。",
  variance:
    "接单方差：衡量骑手接单数量的离散程度；离散系数 CV = 标准差 / 平均接单量。越接近 0 表示数量分配越均匀。",
  zeroRiders:
    "零接单骑手数：本次运行中没有获得任何成功 Assignment 的骑手数量。数值较高可能表示订单较少、空间分布偏斜，或负载分配集中。",
  minMeanMax:
    "接单量 Min / Mean / Max：在全部骑手中统计的最少、平均和最多成功接单数，包含接单数为 0 的骑手。",
  stdDev: "标准差：骑手接单量围绕平均值的波动程度，越小表示分配数量越均匀。",
  maxDistance: "最远匹配距离：所有已匹配订单中观测到的最大匹配距离。",
  assignmentConserved:
    "Assignment 守恒：比较成功 Assignment 数量与全部骑手接单数之和。两者相等表示每个成功分配都准确计入了一名骑手，没有重复或遗漏。",
  bottom10:
    "Bottom 10：按成功接单数从少到多展示最多 10 名骑手；接单数相同时按 Rider UID 排序。用于观察低负载和完全空闲的骑手。",

  // Go Runtime
  allocDelta:
    "堆分配增量：运行期间累计申请过的堆内存总量增量，其中可能包括后来已经被 GC 回收的对象，因此它不是程序结束时仍占用的内存。",
  goroutines:
    "峰值协程数：资源采样期间观测到的最大 goroutine 数量，包括匹配 Worker 及其他运行时协程。",
  gc: "GC 次数与暂停：监测期间垃圾回收发生次数，以及垃圾回收暂停时间的累计值。",
  peakHeapInuse:
    "峰值堆占用：采样期间观测到的最大 HeapInuse，表示已使用的堆 span 空间。",

  // 空间索引
  indexKind: "索引类型：后端实际采用的空间索引实现，例如 kd-tree-node-slice。",
  entryCount:
    "条目总数：空间索引中的骑手条目数。正常情况下应与本次生成的骑手数量相同",
  entrySize:
    "单条大小：KD-Tree 节点结构单个条目的静态大小估算，不包含切片头、额外容量和其他运行时管理开销。",
  estimatedBytes:
    "预估内存：空间索引条目的估算存储开销，不一定包含切片容量、管理结构等额外成本。",

  // 内存与堆栈
  peakHeapAlloc:
    "峰值堆分配：资源采样期间观测到的最大 HeapAlloc，即当时仍分配给堆对象的字节数。",
  peakRuntimeSys:
    "运行时系统内存：运行期间 Go Runtime 从操作系统申请或映射的内存峰值，包括堆、goroutine 栈和运行时管理结构。它不等同于容器监控看到的实际 RSS。",
  peakStackInuse: "峰值栈占用：采样期间观测到的最大 StackInuse。",
  peakStackSys: "峰值栈系统内存：采样期间观测到的最大 StackSys。",
  resourceSamples:
    "资源监控的采样次数和采样间隔。监控包含启动快照和停止时的最终快照。",

  // Balanced
  topK: "候选集大小：为每笔订单搜索最近的 K 名骑手，再从候选中选择更合适的骑手。K 越大，均衡空间越大，计算成本通常越高。",
  maxExtraDistance:
    "最大额外距离：在真正最近骑手距离的基础上，最多允许增加多少米。只有位于这个距离范围内的 Top-K 候选，才会参与负载均衡选择。",
  reorderWindow:
    "重排窗口：为并行候选搜索结果预留的乱序窗口上限，计算方式为 (Channel 容量 + Worker 数) × Batch 大小。Worker 可以乱序完成搜索，但 Coordinator 仍按订单 Sequence 顺序提交分配结果。",
  candidateQueueDepth:
    "最大候选队列深度：候选 Worker 完成搜索后，等待 Coordinator 消费的候选结果队列峰值。数值接近 Channel 容量时，说明 Coordinator 可能成为处理瓶颈。",
  reorderDepth:
    "最大重排深度：Coordinator 为等待缺失的前序 Sequence，而暂存的乱序候选结果最大数量。数值越大，说明并行 Worker 的完成顺序越不一致。",
  candidateWorkers:
    "Candidate Workers：每个 Candidate Worker 完成的 Top-K 候选搜索任务数量。订单之后仍可能因容量不足或分配窗口过期而 deferred，因此该数字不等于成功匹配数。",

  // Deferred
  deferredTotal:
    "Deferred 总计：Balanced 策略中没有产生成功 Assignment、而是进入 Deferred Sink 的订单总数。当前原因包括分配窗口过期和候选骑手容量耗尽。",
  deferredCapacity:
    "容量不足：在 Top-K 和最大额外距离共同限定的候选范围内，所有可选骑手都达到本次运行的累计接单上限，因此无法完成分配的订单数。",
  deferredWindow:
    "超出窗口：Coordinator 准备为订单做最终分配时，Pipeline 已运行到配置的 Assignment Window 之外，因此该订单进入 deferred。",
  sinkTotal:
    "Sink 总计：Memory Deferred Sink 成功记录的订单总数。正常情况下应与 Deferred 总计相等；不相等说明 deferred 记录链路存在遗漏。",
  deferredSamples:
    "抽样数量：为控制响应大小，服务端最多只返回 previewSize 条 deferred 明细；总数以 Sink Total 为准。",
  deferredAtNs:
    "Deferred 时间点：从 Pipeline 启动到该订单被判定为 deferred 所经过的时间，不是该订单自身等待了多久",
} as const;

export const MAX_WORKERS = 32;
export const MAX_BATCH_SIZE = 1024;
export const MAX_CHANNEL_CAPACITY = 1024;
export const MAX_PREVIEW_SIZE = 500;
export const MAX_TOP_K = 100;
export const MAX_EXTRA_DISTANCE_METERS = 50_000;

// ==========================================
// 辅助展示函数 (只读转换，不破坏源数据)
// ==========================================

export function formatNs(ns: number): string {
  if (ns < 1_000_000) {
    return `${(ns / 1_000).toFixed(2)} µs`;
  }
  if (ns < 1_000_000_000) {
    return `${(ns / 1_000_000).toFixed(2)} ms`;
  }
  return `${(ns / 1_000_000_000).toFixed(2)} s`;
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k));
  const val = (bytes / Math.pow(k, i)).toFixed(2);
  return `${val} ${sizes[i]}`;
}

export function formatRate(rate: number): string {
  return Number(rate).toFixed(2);
}

export function formatDistance(meters: number): string {
  if (meters >= 1000) {
    return `${(meters / 1000).toFixed(2)} km`;
  }
  return `${meters.toFixed(1)} m`;
}

export type MetricTooltipKey = keyof typeof METRIC_TOOLTIPS;

export function validateForm(form: MatcherRunRequest): string | null {
  if (form.riderCount <= 0) return "骑手数量必须大于 0";
  if (form.orderCount <= 0) return "订单数量必须大于 0";

  if (form.workers <= 0 || form.workers > MAX_WORKERS) {
    return `Workers 并行数必须在 1 到 ${MAX_WORKERS} 之间`;
  }

  if (form.batchSize <= 0 || form.batchSize > MAX_BATCH_SIZE) {
    return `Batch Size 必须在 1 到 ${MAX_BATCH_SIZE} 之间`;
  }

  if (
    form.channelCapacity <= 0 ||
    form.channelCapacity > MAX_CHANNEL_CAPACITY
  ) {
    return `Channel 容量必须在 1 到 ${MAX_CHANNEL_CAPACITY} 之间`;
  }

  if (form.previewSize < 0 || form.previewSize > MAX_PREVIEW_SIZE) {
    return `抽样数量 (previewSize) 必须在 0 到 ${MAX_PREVIEW_SIZE} 之间`;
  }

  if (form.strategy === "balanced") {
    const maxTopK = Math.min(MAX_TOP_K, form.riderCount);

    if (form.topK <= 0 || form.topK > maxTopK) {
      return `Top-K 必须在 1 到 ${maxTopK} 之间`;
    }

    if (
      form.maxExtraDistanceMeters < 0 ||
      form.maxExtraDistanceMeters > MAX_EXTRA_DISTANCE_METERS
    ) {
      return `最大额外距离必须在 0 到 ${MAX_EXTRA_DISTANCE_METERS} 米之间`;
    }

    if (
      form.maxOrdersPerRider < 0 ||
      form.maxOrdersPerRider > form.orderCount
    ) {
      return `单骑手订单上限必须在 0 到 ${form.orderCount} 之间`;
    }
  }

  return null;
}
