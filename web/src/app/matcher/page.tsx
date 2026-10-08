"use client";

import React, { useState } from "react";
import dynamic from "next/dynamic";
import type {
  MatcherRunRequest,
  MatcherRunResponse,
  MatcherErrorResponse,
  LatencySummary,
  MatcherRetryRequest,
} from "@/components/matcher/contracts";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";

// 1. 动态加载 Leaflet 地图组件，禁用 SSR 并提供占位 Skeleton
const MatcherMap = dynamic(
  () => import("@/components/matcher/MatcherMap").then((mod) => mod.MatcherMap),
  {
    ssr: false,
    loading: () => (
      <div className="w-full h-[420px] md:h-[560px] rounded-lg border border-slate-200 bg-slate-100 animate-pulse flex flex-col items-center justify-center text-slate-400 gap-2">
        <svg
          className="w-8 h-8 animate-spin text-slate-400"
          xmlns="http://www.w3.org/2000/svg"
          fill="none"
          viewBox="0 0 24 24"
        >
          <circle
            className="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            strokeWidth="4"
          />
          <path
            className="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8v8H4z"
          />
        </svg>
        <span className="text-sm font-medium">正在加载地图引擎...</span>
      </div>
    ),
  },
);

// 2. 组件外部定义默认请求参数
const DEFAULT_REQUEST: MatcherRunRequest = {
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

const METRIC_TOOLTIPS = {
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

const MAX_WORKERS = 32;
const MAX_BATCH_SIZE = 1024;
const MAX_CHANNEL_CAPACITY = 1024;
const MAX_PREVIEW_SIZE = 500;
const MAX_TOP_K = 100;
const MAX_EXTRA_DISTANCE_METERS = 50_000;

// ==========================================
// 辅助展示函数 (只读转换，不破坏源数据)
// ==========================================

function formatNs(ns: number): string {
  if (ns < 1_000_000) {
    return `${(ns / 1_000).toFixed(2)} µs`;
  }
  if (ns < 1_000_000_000) {
    return `${(ns / 1_000_000).toFixed(2)} ms`;
  }
  return `${(ns / 1_000_000_000).toFixed(2)} s`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k));
  const val = (bytes / Math.pow(k, i)).toFixed(2);
  return `${val} ${sizes[i]}`;
}

function formatRate(rate: number): string {
  return Number(rate).toFixed(2);
}

function formatDistance(meters: number): string {
  if (meters >= 1000) {
    return `${(meters / 1000).toFixed(2)} km`;
  }
  return `${meters.toFixed(1)} m`;
}

export default function MatcherPage() {
  // 3. 页面四大核心状态
  const [form, setForm] = useState<MatcherRunRequest>(DEFAULT_REQUEST);
  const [result, setResult] = useState<MatcherRunResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState<boolean>(false);

  //
  const [isRetrying, setIsRetrying] = useState(false);
  const [retryMaxOrdersPerRider, setRetryMaxOrdersPerRider] = useState(0);
  const [retryGracePeriodMs, setRetryGracePeriodMs] = useState(30_000);

  // 4. 表单字段统一更新处理
  const updateField = (
    field: keyof MatcherRunRequest,
    rawValue: string | number,
  ) => {
    setForm((prev) => {
      const next = { ...prev };

      // 判定是否属于数字类型字段
      const isNumberField = typeof DEFAULT_REQUEST[field] === "number";
      const value = isNumberField ? Number(rawValue) || 0 : rawValue;

      (next as Record<string, unknown>)[field] = value;

      // 联动策略切换逻辑
      // 联动策略切换逻辑
      if (field === "strategy") {
        if (value === "nearest") {
          next.topK = 0;
          next.maxExtraDistanceMeters = 0;
          next.maxOrdersPerRider = 0;
        } else if (value === "balanced") {
          if (next.topK === 0) next.topK = 5;
          if (next.maxExtraDistanceMeters === 0) {
            next.maxExtraDistanceMeters = 1500;
          }
          next.maxOrdersPerRider = 0;
        }
      }

      return next;
    });
  };

  // 工程参数预设：仅修改 Workers、Batch Size、Channel Capacity
  const applyPreset = (batchSize: number) => {
    setForm((prev) => ({
      ...prev,
      workers: 2,
      batchSize,
      channelCapacity: 16,
    }));
  };

  // 5. 提交前前端校验
  const validateForm = (): string | null => {
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
  };
  //   const validateForm = (): string | null => {
  //     if (form.riderCount <= 0) return "骑手数量必须大于 0";
  //     if (form.orderCount <= 0) return "订单数量必须大于 0";
  //     if (form.workers <= 0) return "Workers 并行数必须大于 0";
  //     if (form.batchSize <= 0) return "Batch Size 必须大于 0";
  //     if (form.channelCapacity <= 0) return "Channel 容量必须大于 0";
  //     if (form.previewSize < 0 || form.previewSize > 500) {
  //       return "抽样数量 (previewSize) 必须在 0 到 500 之间";
  //     }
  //     if (form.strategy === "balanced") {
  //       if (form.topK <= 0) return "均衡策略 (balanced) 下 Top-K 必须大于 0";
  //       if (form.topK > form.riderCount) {
  //         return `Top-K (${form.topK}) 不能超过骑手总数 (${form.riderCount})`;
  //       }
  //       if (form.maxExtraDistanceMeters < 0) return "最大额外距离不能为负数";
  //       if (form.maxOrdersPerRider < 0) return "单骑手订单上限不能为负数";
  //     }
  //     return null;
  //   };

  // 6. 执行实验流程
  const handleRun = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const validationError = validateForm();
    if (validationError) {
      setError(validationError);
      return;
    }

    setIsRunning(true);

    // 规范化 Payload：非 balanced 策略强制清零
    const payload: MatcherRunRequest = {
      ...form,
      topK: form.strategy === "balanced" ? form.topK : 0,
      maxExtraDistanceMeters:
        form.strategy === "balanced" ? form.maxExtraDistanceMeters : 0,
      maxOrdersPerRider:
        form.strategy === "balanced" ? form.maxOrdersPerRider : 0,
    };

    try {
      const res = await fetch("/matcher-api/matcher/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });

      let data: MatcherRunResponse | MatcherErrorResponse;
      try {
        data = await res.json();
      } catch {
        throw new Error("解析响应失败：服务器未返回有效的 JSON");
      }

      if (!res.ok) {
        const errorMsg =
          "error" in data && data.error ? data.error : "未知服务端异常";
        if (res.status === 400) {
          setError(`参数错误 (400): ${errorMsg}`);
        } else if (res.status === 409) {
          setError(`已有实验正在运行 (409): ${errorMsg}`);
        } else if (res.status === 500) {
          setError(`运行失败 (500): ${errorMsg}`);
        } else {
          setError(`请求失败 (${res.status}): ${errorMsg}`);
        }
        return;
      }

      // 每次新实验成功后，重试参数都会从新实验结果初始化，不会继续使用上一场实验遗留的值
      const response = data as MatcherRunResponse;
      const latestRound = response.rounds?.[response.rounds.length - 1];

      setResult(response);
      setRetryMaxOrdersPerRider(response.config.maxOrdersPerRider);
      setRetryGracePeriodMs(latestRound ? latestRound.gracePeriodMs : 30_000);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(`无法连接 matcher API: ${msg}`);
    } finally {
      setIsRunning(false);
    }
  };

  // 获取当前轮次
  const currentRound = result?.rounds?.[result.rounds.length - 1] ?? null;

  const canRetry =
    !!result?.runId &&
    !!currentRound &&
    currentRound.deferredTotal > 0 &&
    !isRunning &&
    !isRetrying;

  const handleRetry = async () => {
    if (!result || !currentRound || !canRetry) return;

    setError(null);

    if (
      !Number.isSafeInteger(retryMaxOrdersPerRider) ||
      retryMaxOrdersPerRider < 0
    ) {
      setError("单骑手订单上限必须是非负整数");
      return;
    }

    if (!Number.isSafeInteger(retryGracePeriodMs) || retryGracePeriodMs < 0) {
      setError("宽限时间必须是非负整数（毫秒）");
      return;
    }

    // guard 和参数校验通过后，创建 Retry 请求
    const payload: MatcherRetryRequest = {
      runId: result.runId,
      sourceRound: currentRound.round,
      maxOrdersPerRider: retryMaxOrdersPerRider,
      gracePeriodMs: retryGracePeriodMs,
    };

    setIsRetrying(true);

    try {
      const res = await fetch("/matcher-api/matcher/retry", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });

      let data: MatcherRunResponse | MatcherErrorResponse;

      try {
        data = await res.json();
      } catch {
        throw new Error("解析响应失败：服务器未返回有效的 JSON");
      }

      if (!res.ok) {
        const message =
          "error" in data && data.error ? data.error : "未知服务端异常";

        if (res.status === 400) {
          setError(`Retry 参数错误 (400): ${message}`);
        } else if (res.status === 404) {
          setError(`运行会话不存在或已被替换 (404): ${message}`);
        } else if (res.status === 409) {
          setError(`当前轮次不可重试或已有实验运行 (409): ${message}`);
        } else if (res.status === 500) {
          setError(`Retry 执行失败 (500): ${message}`);
        } else {
          setError(`Retry 请求失败 (${res.status}): ${message}`);
        }

        return;
      }

      // 服务端返回完整的新状态，直接替换。
      // 如果第 2 轮仍有 Deferred，准备第 3 轮时，输入框就会显示第 2 轮实际采用的参数
      const response = data as MatcherRunResponse;
      const latestRound = response.rounds?.[response.rounds.length - 1];

      setResult(response);

      if (latestRound) {
        setRetryMaxOrdersPerRider(latestRound.maxOrdersPerRider);
        setRetryGracePeriodMs(latestRound.gracePeriodMs);
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      setError(`无法连接 Retry API: ${message}`);
    } finally {
      setIsRetrying(false);
    }
  };

  // 根据策略动态提取纯算法延迟指标与对应标题
  const algorithmLatency = !result
    ? null
    : result.config.strategy === "nearest"
      ? result.performance.algorithmPerformanceMetrics.nearestSearch
      : result.performance.algorithmPerformanceMetrics.algorithmCompute;

  const algorithmLatencyTitle =
    result?.config.strategy === "nearest"
      ? "最近邻搜索"
      : "候选搜索 + 分配决策";

  return (
    <div className="max-w-7xl mx-auto p-4 md:p-8 space-y-8 font-sans">
      {/* 1. 页面标题和简短说明 */}
      <div>
        <h1 className="text-2xl md:text-3xl font-bold tracking-tight text-slate-900">
          订单分配与匹配实验平台 (Matcher Benchmark)
        </h1>
        <p className="text-sm md:text-base text-slate-500 mt-1">
          配置并运行高并发派单仿真管线，测试空间索引、批处理与负载均衡性能。
        </p>
      </div>

      {/* 2. 参数表单 */}
      <form
        onSubmit={handleRun}
        className="bg-white border border-slate-200 rounded-xl p-5 md:p-6 shadow-xs space-y-6"
      >
        <div>
          <h2 className="text-base font-semibold text-slate-800 mb-4 pb-2 border-b border-slate-100">
            基础实验配置
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                骑手总数 (riderCount)
              </label>
              <input
                type="number"
                min="1"
                value={form.riderCount}
                onChange={(e) => updateField("riderCount", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                订单总数 (orderCount)
              </label>
              <input
                type="number"
                min="1"
                value={form.orderCount}
                onChange={(e) => updateField("orderCount", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                到达窗口 (arrivalWindow)
              </label>
              <input
                type="text"
                value={form.arrivalWindow}
                onChange={(e) => updateField("arrivalWindow", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
                placeholder="例如 30s, 1m"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                到达模型 (arrivalModel)
              </label>
              <select
                value={form.arrivalModel}
                onChange={(e) => updateField("arrivalModel", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              >
                <option value="unbounded">unbounded</option>
                <option value="uniform-window">uniform-window</option>
                <option value="front-loaded-burst">front-loaded-burst</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                搜索算法 (algorithm)
              </label>
              <select
                value={form.algorithm}
                onChange={(e) => updateField("algorithm", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              >
                <option value="kd-tree">kd-tree</option>
                <option value="brute-force">brute-force</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                派发策略 (strategy)
              </label>
              <select
                value={form.strategy}
                onChange={(e) => updateField("strategy", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              >
                <option value="nearest">nearest (最近邻匹配)</option>
                <option value="balanced">balanced (负载均衡策略)</option>
              </select>
            </div>
          </div>
        </div>

        {/* 高级参数折叠面板 */}
        <details className="group border border-slate-200 rounded-lg bg-slate-50/50 p-4 open:bg-white transition-colors">
          <summary className="text-sm font-semibold text-slate-700 cursor-pointer select-none group-open:mb-4">
            高级工程与仿真参数
          </summary>

          {/* 工程参数预设 */}
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <span className="text-xs font-medium text-slate-500 mr-1">
              参数预设：
            </span>

            <button
              type="button"
              disabled={isRunning}
              onClick={() => applyPreset(1)}
              className="px-3 py-1.5 text-xs font-medium rounded-md border border-slate-300 bg-white text-slate-700 hover:bg-slate-100 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              低延迟
            </button>

            <button
              type="button"
              disabled={isRunning}
              onClick={() => applyPreset(64)}
              className="px-3 py-1.5 text-xs font-medium rounded-md border border-slate-300 bg-white text-slate-700 hover:bg-slate-100 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              均衡吞吐
            </button>

            <button
              type="button"
              disabled={isRunning}
              onClick={() => applyPreset(256)}
              className="px-3 py-1.5 text-xs font-medium rounded-md border border-slate-300 bg-white text-slate-700 hover:bg-slate-100 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              高吞吐
            </button>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 pt-2">
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                随机种子 (seed)
              </label>
              <input
                type="number"
                value={form.seed}
                onChange={(e) => updateField("seed", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                骑手分布 (riderDistribution)
              </label>
              <select
                value={form.riderDistribution}
                onChange={(e) =>
                  updateField("riderDistribution", e.target.value)
                }
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              >
                <option value="uniform">uniform</option>
                <option value="hotspot">hotspot</option>
                <option value="skewed">skewed</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                订单分布 (orderDistribution)
              </label>
              <select
                value={form.orderDistribution}
                onChange={(e) =>
                  updateField("orderDistribution", e.target.value)
                }
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              >
                <option value="uniform">uniform</option>
                <option value="hotspot">hotspot</option>
                <option value="skewed">skewed</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                Workers 数量
              </label>
              <input
                type="number"
                min={1}
                max={MAX_WORKERS}
                step={1}
                disabled={isRunning}
                value={form.workers}
                onChange={(e) => updateField("workers", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden disabled:opacity-50 disabled:cursor-not-allowed"
              />
              <p className="mt-1 text-xs text-slate-500">
                匹配 Worker goroutine 数。2 核环境建议 1–4
              </p>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                批次大小 (batchSize)
              </label>
              <input
                type="number"
                min={1}
                max={MAX_BATCH_SIZE}
                step={1}
                disabled={isRunning}
                value={form.batchSize}
                onChange={(e) => updateField("batchSize", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden disabled:opacity-50 disabled:cursor-not-allowed"
              />
              <p className="mt-1 text-xs text-slate-500">
                每个 Channel Batch 携带的订单数。大值减少 Channel 通信次数
              </p>
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                Channel 缓冲容量
              </label>
              <input
                type="number"
                min={1}
                max={MAX_CHANNEL_CAPACITY}
                step={1}
                disabled={isRunning}
                value={form.channelCapacity}
                onChange={(e) => updateField("channelCapacity", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden disabled:opacity-50 disabled:cursor-not-allowed"
              />
              <p className="mt-1 text-xs text-slate-500">
                有界 Channel 最多缓存的 Batch 数，用于背压控制
              </p>
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-700 mb-1">
                地图抽样数 (previewSize, 0-{MAX_PREVIEW_SIZE})
              </label>
              <input
                type="number"
                min="0"
                max={MAX_PREVIEW_SIZE}
                value={form.previewSize}
                onChange={(e) => updateField("previewSize", e.target.value)}
                className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
              />
            </div>

            {/* Balanced 专属参数 */}
            {form.strategy === "balanced" && (
              <>
                <div>
                  <label className="block text-xs font-medium text-slate-700 mb-1">
                    候选集大小 (topK)
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={Math.min(MAX_TOP_K, form.riderCount)}
                    step={1}
                    disabled={isRunning}
                    value={form.topK}
                    onChange={(e) => updateField("topK", e.target.value)}
                    className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden disabled:opacity-50 disabled:cursor-not-allowed"
                  />
                  <p className="mt-1 text-xs text-slate-500">
                    先搜索最近的 K 名骑手，再从候选中选择负载更合适的骑手。K
                    越大，均衡空间越大，计算成本越高
                  </p>
                </div>

                <div>
                  <label className="block text-xs font-medium text-slate-700 mb-1">
                    最大额外允许距离 (米)
                  </label>
                  <input
                    type="number"
                    min="0"
                    max={MAX_EXTRA_DISTANCE_METERS}
                    value={form.maxExtraDistanceMeters}
                    onChange={(e) =>
                      updateField("maxExtraDistanceMeters", e.target.value)
                    }
                    className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-slate-700 mb-1">
                    单骑手订单上限 (maxOrdersPerRider) (0=不设限)
                  </label>
                  <input
                    type="number"
                    min="0"
                    max={form.orderCount}
                    value={form.maxOrdersPerRider}
                    onChange={(e) =>
                      updateField("maxOrdersPerRider", e.target.value)
                    }
                    className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md bg-white focus:ring-2 focus:ring-blue-500 focus:outline-hidden"
                  />
                </div>
              </>
            )}
          </div>
        </details>

        <div className="flex justify-end pt-2">
          <button
            type="submit"
            disabled={isRunning || isRetrying}
            className="px-6 py-2.5 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-300 text-white font-medium text-sm rounded-lg shadow-xs transition-colors cursor-pointer disabled:cursor-not-allowed flex items-center gap-2"
          >
            {isRunning && (
              <svg
                className="w-4 h-4 animate-spin text-white"
                xmlns="http://www.w3.org/2000/svg"
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle
                  className="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  strokeWidth="4"
                />
                <path
                  className="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8v8H4z"
                />
              </svg>
            )}
            {isRunning ? "运行中..." : "启动仿真匹配"}
          </button>
        </div>
      </form>

      {/* 3. 错误提示条 */}
      {error && (
        <div className="p-4 rounded-lg bg-red-50 border border-red-200 text-red-700 text-sm flex items-start gap-3">
          <span className="font-bold">提示：</span>
          <div className="break-all">{error}</div>
        </div>
      )}

      {/* 4. 实验结果展示区 */}
      {result && (
        <div className="space-y-6">
          {/* 地图组件展示 */}
          <div className="bg-white border border-slate-200 rounded-xl p-4 shadow-xs">
            <h2 className="text-base font-semibold text-slate-800 mb-3">
              空间位置与派单采样视图
            </h2>
            <MatcherMap data={result.map} />
          </div>
          {/* 5. 基础计数卡片 (第一排) */}
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Generated (已生成)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-slate-800">
                  {result.counts.generated.toLocaleString()}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Admitted (已接纳)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-slate-800">
                  {result.counts.admitted.toLocaleString()}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Completed (已完成)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-emerald-600">
                  {result.counts.completed.toLocaleString()}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Unfinished (未完成)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-amber-600">
                  {result.counts.unfinished.toLocaleString()}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Deferred (推迟排队)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-slate-600">
                  {result.counts.deferred.toLocaleString()}
                </div>
              </CardContent>
            </Card>
          </div>
          {/* 6. 守恒与吞吐卡片 (第二排) */}
          <div className="grid grid-cols-2 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Completion Conserved
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1 flex items-center gap-2">
                <span
                  className={`text-lg font-bold ${
                    result.counts.completionConserved
                      ? "text-emerald-600"
                      : "text-red-600"
                  }`}
                >
                  {result.counts.completionConserved ? "满足守恒" : "不守恒"}
                </span>
                <span
                  className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                    result.counts.completionConserved
                      ? "bg-emerald-50 text-emerald-700 border border-emerald-200"
                      : "bg-red-50 text-red-700 border border-red-200"
                  }`}
                >
                  {String(result.counts.completionConserved)}
                </span>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Deferred Conserved
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1 flex items-center gap-2">
                <span
                  className={`text-lg font-bold ${
                    result.counts.deferredConserved
                      ? "text-emerald-600"
                      : "text-red-600"
                  }`}
                >
                  {result.counts.deferredConserved ? "满足守恒" : "不守恒"}
                </span>
                <span
                  className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                    result.counts.deferredConserved
                      ? "bg-emerald-50 text-emerald-700 border border-emerald-200"
                      : "bg-red-50 text-red-700 border border-red-200"
                  }`}
                >
                  {String(result.counts.deferredConserved)}
                </span>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Throughput (吞吐量)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-blue-600">
                  {formatRate(result.timing.throughputPerSecond)}{" "}
                  <span className="text-xs text-slate-500 font-normal">
                    ops/s
                  </span>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="p-4 pb-1">
                <CardTitle className="text-xs font-normal text-slate-500">
                  Total Time (总耗时)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-1">
                <div className="text-xl font-bold text-slate-800">
                  {formatNs(result.timing.totalNs)}
                </div>
              </CardContent>
            </Card>
          </div>
          {/* 7. 延迟指标展示 */}
          <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-4">
            <h3 className="text-base font-semibold text-slate-800">
              流水线延迟与耗时分布 (Latency Metrics)
            </h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              <LatencyBox
                title="端到端延迟 (End To End)"
                metric="endToEnd"
                data={result.performance.endToEndLatency}
              />
              <LatencyBox
                title="排队与匹配 (Queue & Match)"
                metric="queueAndMatch"
                data={result.performance.queueAndMatchLatency}
              />
              <LatencyBox
                title="准入延迟 (Admission Delay)"
                metric="admissionDelay"
                data={result.performance.admissionDelay}
              />
              <LatencyBox
                title={algorithmLatencyTitle}
                metric={
                  result.config.strategy === "nearest"
                    ? "nearestSearch"
                    : "algorithmCompute"
                }
                data={algorithmLatency!}
              />
            </div>

            <div className="text-xs text-slate-500 grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 pt-3 border-t border-slate-100">
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="索引构建" metric="indexBuild" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.timing.indexBuildNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="骑手生成" metric="riderGeneration" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.timing.riderGenerationNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="pipeline执行" metric="pipeline" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.timing.pipelineNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="准入速率" metric="admissionRate" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatRate(result.performance.actualAdmissionRatePerSecond)}{" "}
                  req/s
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="完成速率" metric="completionRate" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatRate(result.performance.actualCompletionRatePerSecond)}{" "}
                  req/s
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip
                    label="实际注入时间"
                    metric="actualInjection"
                  />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.performance.actualInjectionNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="注入超时" metric="injectionOverrun" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.performance.injectionOverrunNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="窗口后排空" metric="drainAfterWindow" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.performance.drainAfterWindowNs)}
                </span>
              </div>
              <div>
                <span className="text-slate-400 block">
                  <MetricTooltip label="Pipeline 总运行" metric="totalRun" />
                </span>
                <span className="font-medium text-slate-700">
                  {formatNs(result.performance.totalRunNs)}
                </span>
              </div>
            </div>
          </div>
          {/* 8. 公平性与系统资源 */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-4">
              <h3 className="text-base font-semibold text-slate-800">
                负载均衡与公平性 (Fairness)
              </h3>

              {/* 8 项核心指标网格 */}
              <div className="grid grid-cols-2 sm:grid-cols-2 gap-3 text-sm">
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">平均订单距离</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatDistance(result.fairness.averageDistanceMeters)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">P95 订单距离</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatDistance(result.fairness.p95DistanceMeters)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    骑手接单方差 / 离散系数
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.fairness.varianceOrders.toFixed(2)} /{" "}
                    {result.fairness.coefficientOfVariation.toFixed(3)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">零接单骑手数</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.fairness.zeroRiderCount}
                  </div>
                </div>

                {/* 新增的 4 项小格 */}
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    接单量 Min / Mean / Max
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.fairness.minOrders} /{" "}
                    {result.fairness.meanOrders.toFixed(1)} /{" "}
                    {result.fairness.maxOrders}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">标准差 (StdDev)</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.fairness.stdDevOrders.toFixed(2)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="最远匹配距离" metric="maxDistance" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatDistance(result.fairness.maxDistanceMeters)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="Assignment 守恒"
                      metric="assignmentConserved"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.fairness.assignmentCount} /{" "}
                    {result.fairness.riderOrderCountSum}
                  </div>
                </div>
              </div>

              {/* 底部 Bottom 10 骑手统计表格 */}
              <div className="pt-2 border-t border-slate-100">
                <div className="text-xs font-semibold text-slate-700 mb-2">
                  <MetricTooltip
                    label="接单垫底骑手统计 (Bottom 10)"
                    metric="bottom10"
                  />
                </div>
                {result.fairness.bottom10 &&
                result.fairness.bottom10.length > 0 ? (
                  <div className="overflow-x-auto rounded-lg border border-slate-100">
                    <table className="w-full text-left text-xs">
                      <thead className="bg-slate-50 text-slate-600 border-b border-slate-100">
                        <tr>
                          <th className="px-3 py-2 font-medium">Rider UID</th>
                          <th className="px-3 py-2 font-medium text-right">
                            Order Count
                          </th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-100 font-mono">
                        {result.fairness.bottom10.map((rider) => (
                          <tr
                            key={rider.riderUid}
                            className="hover:bg-slate-50/50"
                          >
                            <td className="px-3 py-1.5 text-slate-700">
                              {rider.riderUid}
                            </td>
                            <td className="px-3 py-1.5 text-slate-700 text-right">
                              {rider.orderCount}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="py-4 text-center text-xs text-slate-400 bg-slate-50/50 rounded-lg border border-dashed border-slate-200">
                    没有可展示的骑手统计
                  </div>
                )}
              </div>
            </div>

            <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-3">
              <h3 className="text-base font-semibold text-slate-800">
                运行时资源监控 (Go Runtime Resources)
              </h3>
              <div className="grid grid-cols-2 gap-3 text-sm">
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="堆分配增量 (Alloc Delta)"
                      metric="allocDelta"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.totalAllocDeltaBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="峰值协程数 (Goroutines)"
                      metric="goroutines"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.resources.peakGoroutines}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="GC 发生次数 / 暂停累计" metric="gc" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.resources.numGCDelta} 次 /{" "}
                    {formatNs(result.resources.gcPauseDeltaNs)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="峰值堆占用 (Peak Heap Inuse)"
                      metric="peakHeapInuse"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakHeapInuseBytes)}
                  </div>
                </div>
              </div>
            </div>
          </div>
          {/* 8.5 索引与内存详情卡片 */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {/* 索引结构详情 */}
            <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-3">
              <h3 className="text-base font-semibold text-slate-800">
                空间索引结构 (Index Details)
              </h3>
              <div className="grid grid-cols-2 gap-3 text-sm">
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="索引类型 (Kind)" metric="indexKind" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5 font-mono">
                    {result.index.kind}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="条目总数 (Entry Count)"
                      metric="entryCount"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.index.entryCount.toLocaleString()}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="单条大小 (Entry Size)"
                      metric="entrySize"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.index.entrySizeBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="预估内存 (Estimated Bytes)"
                      metric="estimatedBytes"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.index.estimatedBytes)}
                  </div>
                </div>
              </div>
            </div>

            {/* 内存与堆栈画像 */}
            <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-3">
              <h3 className="text-base font-semibold text-slate-800">
                内存与堆栈画像 (Memory & Stack Profile)
              </h3>
              <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 text-sm">
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="峰值堆分配" metric="peakHeapAlloc" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakHeapAllocBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="峰值堆占用" metric="peakHeapInuse" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakHeapInuseBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="运行时系统内存"
                      metric="peakRuntimeSys"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakRuntimeSysBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="峰值栈占用" metric="peakStackInuse" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakStackInuseBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="峰值栈系统内存"
                      metric="peakStackSys"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatBytes(result.resources.peakStackSysBytes)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip
                      label="采样样本数 / 间隔"
                      metric="resourceSamples"
                    />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5 text-xs">
                    {result.resources.samples} 次 /{" "}
                    {result.resources.sampleInterval}
                  </div>
                </div>
              </div>
            </div>
          </div>
          {/* 8.6 Strategy B 均衡策略专属卡片（仅当存在时渲染） */}
          {result.strategy && (
            <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-4">
              <h3 className="text-base font-semibold text-slate-800">
                均衡派单策略详情 (Strategy B - Balanced)
              </h3>

              {/* 5 项核心策略参数 */}
              <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 text-sm">
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    候选集大小 (Top-K)
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.strategy.topK}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">最大额外距离</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {formatDistance(result.strategy.maxExtraDistanceMeters)}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    重排窗口 (Reorder Window)
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.strategy.reorderWindow}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">最大候选队列深度</div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.strategy.maxCandidateQueueDepth}
                  </div>
                </div>
                <div className="p-3 bg-slate-50 rounded-lg">
                  <div className="text-xs text-slate-500">
                    <MetricTooltip label="最大重排深度" metric="reorderDepth" />
                  </div>
                  <div className="font-semibold text-slate-800 mt-0.5">
                    {result.strategy.maxReorderDepth}
                  </div>
                </div>
              </div>

              {/* Candidate Worker 吞吐分布表格 */}
              <div className="pt-2 border-t border-slate-100">
                <div className="text-xs font-semibold text-slate-700 mb-2">
                  <MetricTooltip
                    label="Worker 处理完成单量分布 (Candidate Workers)"
                    metric="candidateWorkers"
                  />
                </div>
                {result.strategy.candidateWorkerCompletedOrders &&
                result.strategy.candidateWorkerCompletedOrders.length > 0 ? (
                  <div className="overflow-x-auto rounded-lg border border-slate-100">
                    <table className="w-full text-left text-xs">
                      <thead className="bg-slate-50 text-slate-600 border-b border-slate-100">
                        <tr>
                          <th className="px-3 py-2 font-medium">Worker 编号</th>
                          <th className="px-3 py-2 font-medium text-right">
                            Completed Orders
                          </th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-100 font-mono">
                        {result.strategy.candidateWorkerCompletedOrders.map(
                          (completedCount, workerIndex) => (
                            <tr
                              key={workerIndex}
                              className="hover:bg-slate-50/50"
                            >
                              <td className="px-3 py-1.5 text-slate-700">
                                Worker #{workerIndex}
                              </td>
                              <td className="px-3 py-1.5 text-slate-700 text-right">
                                {completedCount.toLocaleString()}
                              </td>
                            </tr>
                          ),
                        )}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="py-4 text-center text-xs text-slate-400 bg-slate-50/50 rounded-lg border border-dashed border-slate-200">
                    无 Worker 完成记录
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 处理轮次历史 */}
          {result.rounds && result.rounds.length > 0 && (
            <section className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-5">
              <div>
                <h3 className="text-base font-semibold text-slate-800">
                  处理轮次
                </h3>
                <p className="mt-1 text-xs text-slate-500">
                  Run ID：{result.runId} · 共 {result.rounds.length} 轮
                </p>
              </div>

              <div className="space-y-5">
                {result.rounds.map((round, index) => {
                  const isCurrent = index === result.rounds.length - 1;
                  const previousRound =
                    index > 0 ? result.rounds[index - 1] : null;

                  const successRate =
                    round.inputCount > 0
                      ? (round.matchedCount / round.inputCount) * 100
                      : 0;

                  const inputConserved =
                    previousRound === null ||
                    round.inputCount === previousRound.deferredTotal;

                  const formatLimit = (value: number) =>
                    value === 0 ? "不限制" : String(value);

                  const stats = [
                    { label: "输入订单", value: round.inputCount },
                    { label: "匹配成功", value: round.matchedCount },
                    { label: "Deferred", value: round.deferredTotal },
                    { label: "容量不足", value: round.deferredCapacity },
                    { label: "窗口过期", value: round.deferredWindow },
                  ];

                  return (
                    <div key={round.round} className="space-y-3">
                      {previousRound && (
                        <div className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs space-y-2">
                          <div className="font-semibold text-slate-700">
                            从第 {previousRound.round} 轮重放
                          </div>

                          <div className="flex flex-wrap gap-x-6 gap-y-2 text-slate-600">
                            <span>
                              骑手上限：
                              {formatLimit(previousRound.maxOrdersPerRider)}
                              {" → "}
                              {formatLimit(round.maxOrdersPerRider)}
                            </span>

                            <span>
                              宽限时间：
                              {previousRound.gracePeriodMs / 1000} 秒{" → "}
                              {round.gracePeriodMs / 1000} 秒
                            </span>

                            <span>
                              重试输入：
                              {previousRound.deferredTotal.toLocaleString()} 笔
                            </span>
                          </div>

                          <div
                            className={
                              inputConserved
                                ? "font-medium text-emerald-700"
                                : "font-medium text-red-700"
                            }
                          >
                            {inputConserved
                              ? "✓ Deferred 输入守恒"
                              : "✕ 重试输入数量不一致"}
                            {" · "}
                            上轮 Deferred：
                            {previousRound.deferredTotal.toLocaleString()}
                            {" · "}
                            本轮输入：
                            {round.inputCount.toLocaleString()}
                          </div>
                        </div>
                      )}

                      <div
                        className={`rounded-lg border p-4 space-y-4 ${
                          isCurrent
                            ? "border-blue-200 bg-blue-50/30"
                            : "border-slate-200 bg-white"
                        }`}
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex items-center gap-2">
                            <span
                              className={`h-2.5 w-2.5 rounded-full ${
                                isCurrent ? "bg-blue-600" : "bg-slate-400"
                              }`}
                            />
                            <h4 className="text-sm font-semibold text-slate-800">
                              第 {round.round} 轮
                            </h4>
                          </div>

                          <span
                            className={`rounded-full px-2.5 py-1 text-xs font-medium ${
                              isCurrent
                                ? "bg-blue-100 text-blue-700"
                                : "bg-slate-100 text-slate-600"
                            }`}
                          >
                            {isCurrent ? "当前轮次" : "已完成"}
                          </span>
                        </div>

                        <p className="text-xs text-slate-600">
                          输入来源：
                          {round.inputSource === "generated"
                            ? "新生成订单"
                            : round.inputSource === "deferred"
                              ? `来自第 ${round.sourceRound} 轮 Deferred`
                              : round.inputSource}
                        </p>

                        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
                          {stats.map((stat) => (
                            <div
                              key={stat.label}
                              className="rounded-lg bg-slate-50 border border-slate-100 p-3"
                            >
                              <div className="text-xs text-slate-500">
                                {stat.label}
                              </div>
                              <div className="mt-1 text-lg font-semibold text-slate-800">
                                {stat.value.toLocaleString()}
                              </div>
                            </div>
                          ))}

                          <div className="rounded-lg bg-slate-50 border border-slate-100 p-3">
                            <div className="text-xs text-slate-500">成功率</div>
                            <div className="mt-1 text-lg font-semibold text-slate-800">
                              {successRate.toFixed(2)}%
                            </div>
                          </div>
                        </div>

                        <div className="flex flex-wrap gap-x-6 gap-y-2 border-t border-slate-100 pt-3 text-xs text-slate-600">
                          <span>
                            骑手上限：
                            <strong className="ml-1 text-slate-800">
                              {formatLimit(round.maxOrdersPerRider)}
                            </strong>
                          </span>

                          <span>
                            宽限时间：
                            <strong className="ml-1 text-slate-800">
                              {round.gracePeriodMs / 1000} 秒
                            </strong>
                          </span>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          )}

          {/* Retry：仅当前轮存在 Deferred 时显示 */}
          {result?.runId &&
            currentRound &&
            currentRound.deferredTotal > 0 &&
            !isRunning && (
              <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-4">
                <div>
                  <h3 className="text-base font-semibold text-slate-800">
                    Deferred 订单重试
                  </h3>
                  <p className="mt-1 text-xs text-slate-500">
                    当前第 {currentRound.round} 轮，剩余{" "}
                    {currentRound.deferredTotal.toLocaleString()} 笔 Deferred
                    订单。 本功能重放上一轮 Deferred 订单并保留订单
                    ID；服务端会使用相同 seed
                    重新生成骑手，但不会继承上一轮骑手接单负载。它用于比较调整容量后的结果。
                  </p>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div>
                    <label
                      htmlFor="retry-max-orders"
                      className="block text-xs font-medium text-slate-700 mb-1"
                    >
                      新的单骑手订单上限 (maxOrdersPerRider)
                    </label>
                    <input
                      id="retry-max-orders"
                      type="number"
                      min={0}
                      step={1}
                      value={retryMaxOrdersPerRider}
                      onChange={(e) =>
                        setRetryMaxOrdersPerRider(Number(e.target.value))
                      }
                      className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:outline-none"
                    />
                    <p className="mt-1 text-xs text-slate-500">
                      0 表示不限制；本参数仅影响下一轮。
                    </p>
                  </div>

                  <div>
                    <label
                      htmlFor="retry-grace-period"
                      className="block text-xs font-medium text-slate-700 mb-1"
                    >
                      重试宽限时间 (gracePeriodMs)
                    </label>
                    <input
                      id="retry-grace-period"
                      type="number"
                      min={0}
                      step={1}
                      value={retryGracePeriodMs}
                      onChange={(e) =>
                        setRetryGracePeriodMs(Number(e.target.value))
                      }
                      className="w-full px-3 py-2 text-sm border border-slate-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:outline-none"
                    />
                    <p className="mt-1 text-xs text-slate-500">
                      单位为毫秒；0 表示沿用上一轮的 ArrivalWindow。
                    </p>
                  </div>
                </div>

                <div className="flex justify-end">
                  <button
                    type="button"
                    onClick={handleRetry}
                    disabled={!canRetry}
                    className="px-5 py-2.5 rounded-lg bg-amber-600 text-white text-sm font-medium hover:bg-amber-700 disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {isRetrying
                      ? "正在重试..."
                      : `重试第 ${currentRound.round + 1} 轮`}
                  </button>
                </div>
              </div>
            )}
          {/* 8.7 Deferred 延迟订单卡片 */}
          <div className="bg-white border border-slate-200 rounded-xl p-5 shadow-xs space-y-4">
            <h3 className="text-base font-semibold text-slate-800">
              当前轮 Deferred 明细
            </h3>
            <p className="mt-1 text-xs text-slate-500">
              第 {result.currentRound} 轮 · Deferred Details
            </p>

            {/* 汇总区：始终展示 */}
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 text-sm">
              <div className="p-3 bg-slate-50 rounded-lg">
                <div className="text-xs text-slate-500">
                  <MetricTooltip label="Deferred 总计" metric="deferredTotal" />
                </div>
                <div className="font-semibold text-slate-800 mt-0.5">
                  {result.counts.deferred.toLocaleString()}
                </div>
              </div>
              <div className="p-3 bg-slate-50 rounded-lg">
                <div className="text-xs text-slate-500">
                  <MetricTooltip label="容量不足" metric="deferredCapacity" />
                </div>
                <div className="font-semibold text-amber-600 mt-0.5">
                  {result.counts.deferredByCapacity.toLocaleString()}
                </div>
              </div>
              <div className="p-3 bg-slate-50 rounded-lg">
                <div className="text-xs text-slate-500">
                  <MetricTooltip label="超出窗口" metric="deferredWindow" />
                </div>
                <div className="font-semibold text-rose-600 mt-0.5">
                  {result.counts.deferredByWindow.toLocaleString()}
                </div>
              </div>
              <div className="p-3 bg-slate-50 rounded-lg">
                <div className="text-xs text-slate-500">
                  <MetricTooltip label="Sink 总计" metric="sinkTotal" />
                </div>
                <div className="font-semibold text-slate-800 mt-0.5">
                  {result.deferred.total.toLocaleString()}
                </div>
              </div>
              <div className="p-3 bg-slate-50 rounded-lg">
                <div className="text-xs text-slate-500">
                  <MetricTooltip label="抽样数量" metric="deferredSamples" />
                </div>
                <div className="font-semibold text-slate-800 mt-0.5">
                  {result.deferred.orders.length.toLocaleString()}
                </div>
              </div>
            </div>

            {/* 明细表区 */}
            <div className="pt-2 border-t border-slate-100">
              <div className="text-xs font-semibold text-slate-700 mb-2">
                延迟订单抽样明细
              </div>
              {result.deferred.orders && result.deferred.orders.length > 0 ? (
                <div className="overflow-x-auto rounded-lg border border-slate-100">
                  <table className="w-full text-left text-xs">
                    <thead className="bg-slate-50 text-slate-600 border-b border-slate-100">
                      <tr>
                        <th className="px-3 py-2 font-medium">Sequence</th>
                        <th className="px-3 py-2 font-medium">原因</th>
                        <th className="px-3 py-2 font-medium">Attempt</th>
                        <th className="px-3 py-2 font-medium">延迟时间</th>
                        <th className="px-3 py-2 font-medium">经纬度坐标</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-100 font-mono">
                      {result.deferred.orders.map((item, index) => (
                        <tr
                          key={`deferred-${item.order.sequence}-${index}`}
                          className="hover:bg-slate-50/50"
                        >
                          <td className="px-3 py-1.5 text-slate-700 font-semibold">
                            #{item.order.sequence}
                          </td>
                          <td className="px-3 py-1.5 text-slate-600 font-sans">
                            {item.reason}
                          </td>
                          <td className="px-3 py-1.5 text-slate-700">
                            {item.attempt}
                          </td>
                          <td className="px-3 py-1.5 text-slate-700">
                            {formatNs(item.deferredAtNs)}
                          </td>
                          <td className="px-3 py-1.5 text-slate-500">
                            {item.order.pickup.latitude.toFixed(5)},{" "}
                            {item.order.pickup.longitude.toFixed(5)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="py-4 text-center text-xs text-slate-400 bg-slate-50/50 rounded-lg border border-dashed border-slate-200">
                  本次运行没有 deferred 抽样订单。
                </div>
              )}
            </div>
          </div>

          {/* 9. 原始 / 解析后 JSON 折叠面板 */}
          <details className="border border-slate-200 rounded-xl bg-slate-50 p-4">
            <summary className="text-sm font-semibold text-slate-700 cursor-pointer select-none">
              解析后的运行结果 JSON (Parsed Response JSON)
            </summary>
            <div className="mt-1 text-xs text-slate-500">
              提示：浏览器 JSON 反序列化过程可能对 uint64 订单 ID
              造成精度裁剪，关键业务关联请以 sequence 为准。
            </div>
            <pre className="mt-3 p-4 bg-slate-900 text-slate-100 rounded-lg text-xs overflow-x-auto max-h-96 overflow-y-auto font-mono">
              {JSON.stringify(result, null, 2)}
            </pre>
          </details>
        </div>
      )}
    </div>
  );
}

type MetricTooltipKey = keyof typeof METRIC_TOOLTIPS;

function MetricTooltip({
  label,
  metric,
}: {
  label: string;
  metric: MetricTooltipKey;
}) {
  return (
    <span className="group relative inline-flex max-w-full items-center gap-1 align-middle">
      <span>{label}</span>

      <span
        tabIndex={0}
        aria-label={`${label}：${METRIC_TOOLTIPS[metric]}`}
        className="inline-flex shrink-0 cursor-help items-center justify-center text-slate-400 transition-colors hover:text-blue-600 focus:text-blue-600 focus:outline-none"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" />
          <path d="M12 16v-4" />
          <path d="M12 8h.01" />
        </svg>
      </span>

      <span
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-0 z-50 mb-2 hidden w-72 max-w-[85vw] rounded-lg bg-slate-900 px-3 py-2.5 text-left text-xs font-normal leading-relaxed whitespace-normal text-white shadow-xl group-hover:block group-focus-within:block"
      >
        {METRIC_TOOLTIPS[metric]}
      </span>
    </span>
  );
}

// 延迟信息微组件

function LatencyBox({
  title,
  data,
  metric,
}: {
  title: string;
  data: LatencySummary;
  metric: MetricTooltipKey;
}) {
  const fields = [
    { label: "Min", key: "min", value: formatNs(data.minNs) },
    { label: "P50", key: "p50", value: formatNs(data.p50Ns) },
    { label: "P95", key: "p95", value: formatNs(data.p95Ns) },
    { label: "P99", key: "p99", value: formatNs(data.p99Ns) },
    { label: "Max", key: "max", value: formatNs(data.maxNs) },
    { label: "Count", key: "count", value: String(data.count) },
  ] as const;

  return (
    <div className="border border-slate-100 bg-slate-50/70 p-3.5 rounded-lg space-y-2">
      <div className="text-xs font-semibold text-slate-700">
        <MetricTooltip label={title} metric={metric} />
      </div>

      <div className="grid grid-cols-3 gap-2 text-xs">
        {fields.map((field) => (
          <div key={field.key} className="min-w-0">
            <div className="text-slate-400">
              <MetricTooltip label={field.label} metric={field.key} />
            </div>

            <span className="font-mono text-slate-700">{field.value}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
