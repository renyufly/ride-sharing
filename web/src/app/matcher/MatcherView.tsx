"use client";

import dynamic from "next/dynamic";
import type { LatencySummary } from "@/components/matcher/contracts";
import {
  METRIC_TOOLTIPS,
  MAX_WORKERS,
  MAX_BATCH_SIZE,
  MAX_CHANNEL_CAPACITY,
  MAX_PREVIEW_SIZE,
  MAX_TOP_K,
  MAX_EXTRA_DISTANCE_METERS,
  formatNs,
  formatBytes,
  formatRate,
  formatDistance,
} from "./matcher_helpers";
import type { useMatcherPage } from "./useMatcherPage";
import type { MetricTooltipKey } from "./matcher_helpers";
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

type MatcherPageModel = ReturnType<typeof useMatcherPage>;

type MatcherViewProps = Omit<MatcherPageModel, "handleRun" | "handleRetry"> & {
  onMatch: MatcherPageModel["handleRun"];
  onRetry: MatcherPageModel["handleRetry"];
};

export function MatcherView({
  form,
  result,
  error,
  isRunning,
  isRetrying,
  retryMaxOrdersPerRider,
  retryGracePeriodMs,
  currentRound,
  canRetry,
  algorithmLatency,
  algorithmLatencyTitle,
  updateField,
  applyPreset,
  onMatch,
  onRetry,
  changeRetryMaxOrdersPerRider,
  changeRetryGracePeriodMs,
}: MatcherViewProps) {
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
        onSubmit={onMatch}
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
                        changeRetryMaxOrdersPerRider(Number(e.target.value))
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
                        changeRetryGracePeriodMs(Number(e.target.value))
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
                    onClick={onRetry}
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
