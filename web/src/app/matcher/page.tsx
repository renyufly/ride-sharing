"use client";

import { useState } from "react";
import { useMatcherPage } from "./useMatcherPage";
import { MatcherView } from "./MatcherView";
import { LiveDispatchDemo } from "./LiveDispatchDemo";

export default function MatcherPage() {
  const [mode, setMode] = useState<"live" | "simulation">("live");
  const { handleRun, handleRetry, ...viewData } = useMatcherPage();

  return (
    <main className="min-h-screen bg-slate-50">
      <div className="mx-auto max-w-7xl px-4 pt-8 md:px-8">
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-blue-700">
          Matcher
        </p>
        <h1 className="mt-2 text-3xl font-bold tracking-tight text-slate-900">
          订单匹配与实时派单
        </h1>
        <p className="mt-2 max-w-3xl text-sm text-slate-600">
          两种入口共用候选搜索能力。实时派单还会通过骑手长连接等待接单，并在拒单后重新选择骑手。
        </p>
        <div role="tablist" aria-label="匹配模式" className="mt-6 flex gap-2 border-b border-slate-200">
          <button
            id="live-tab"
            type="button"
            role="tab"
            aria-selected={mode === "live"}
            aria-controls="live-panel"
            onClick={() => setMode("live")}
            className={`rounded-t-lg px-5 py-3 text-sm font-semibold transition-colors ${mode === "live" ? "border-b-2 border-blue-600 bg-white text-blue-700" : "text-slate-500 hover:bg-white hover:text-slate-800"}`}
          >
            实时派单
          </button>
          <button
            id="simulation-tab"
            type="button"
            role="tab"
            aria-selected={mode === "simulation"}
            aria-controls="simulation-panel"
            onClick={() => setMode("simulation")}
            className={`rounded-t-lg px-5 py-3 text-sm font-semibold transition-colors ${mode === "simulation" ? "border-b-2 border-blue-600 bg-white text-blue-700" : "text-slate-500 hover:bg-white hover:text-slate-800"}`}
          >
            模拟匹配与性能分析
          </button>
        </div>
      </div>
      <div id="live-panel" role="tabpanel" aria-labelledby="live-tab" hidden={mode !== "live"}>
        <LiveDispatchDemo />
      </div>
      <div id="simulation-panel" role="tabpanel" aria-labelledby="simulation-tab" hidden={mode !== "simulation"}>
        <div className="mx-auto max-w-7xl px-4 pt-8 md:px-8">
          <div className="rounded-xl border border-slate-200 bg-white p-5 text-sm text-slate-700">
            <h2 className="font-semibold text-slate-900">模拟匹配流水线</h2>
            <p className="mt-2">
              Balanced 策略使用 Producer、Batch 与对象池、并行 Top-K 候选搜索 worker，
              再由协调器完成负载均衡和统计。这里分析吞吐、延迟、公平性与 Deferred 重试；
              骑手的 WebSocket 接单流程请切换到“实时派单”。
            </p>
          </div>
        </div>
        {mode === "simulation" && (
          <MatcherView {...viewData} onMatch={handleRun} onRetry={handleRetry} />
        )}
      </div>
    </main>
  );
}
