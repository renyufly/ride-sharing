"use client";

import { useState } from "react";

const REPLICAS_URL = "/matcher-api/matcher/admin/replicas";

type ReplicaStatus = { desired: number; ready: number; maxReplicas: number };
const statusTimeFormat = new Intl.DateTimeFormat("zh-CN", { timeStyle: "medium", hourCycle: "h23" });

async function requestReplicas(token: string, replicas?: number): Promise<unknown> {
  const response = await fetch(REPLICAS_URL, {
    method: replicas === undefined ? "GET" : "PUT",
    headers: {
      Authorization: `Bearer ${token}`,
      ...(replicas === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: replicas === undefined ? undefined : JSON.stringify({ replicas }),
    cache: "no-store",
    signal: AbortSignal.timeout(10_000),
  });
  if (response.status === 401) throw new Error("管理 token 无效，请检查后重试。");
  if (response.status === 409) throw new Error("副本设置刚被修改，请刷新状态后重试。");
  const data = await response.json().catch(() => null);
  if (!response.ok) throw new Error(data?.error ?? `管理接口返回 HTTP ${response.status}`);
  if (replicas !== undefined && (response.status !== 202 || data?.desired !== replicas)) {
    throw new Error("管理接口返回了无效的扩缩容回执");
  }
  return data;
}

export function MatcherReplicaControls() {
  const [token, setToken] = useState("");
  const [status, setStatus] = useState<(ReplicaStatus & { checkedAt: number }) | null>(null);
  const [requested, setRequested] = useState<number | null>(null);
  const [target, setTarget] = useState("");
  const [operation, setOperation] = useState<"read" | "scale" | null>(null);
  const [error, setError] = useState("");
  const count = Number(target);
  const busy = operation !== null;
  const configuredTarget = requested ?? status?.desired;
  const validTarget = status !== null && Number.isInteger(count) && count >= 1 && count <= status.maxReplicas;
  const atTarget = status !== null && requested === null && status.ready === status.desired;

  async function updateReplicas(replicas?: number) {
    setOperation(replicas === undefined ? "read" : "scale");
    setError("");
    try {
      const data = await requestReplicas(token.trim(), replicas);
      if (replicas !== undefined) {
        setRequested(replicas);
        return;
      }
      const next = data as Partial<ReplicaStatus> | null;
      if (typeof next?.desired !== "number" || typeof next.ready !== "number" || typeof next.maxReplicas !== "number") {
        throw new Error("管理接口返回了无效的副本状态。");
      }
      setStatus({ desired: next.desired, ready: next.ready, maxReplicas: next.maxReplicas, checkedAt: Date.now() });
      setRequested(null);
      if (!status || count === configuredTarget) setTarget(String(next.desired));
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      setError(replicas === undefined ? message : `${message} 请刷新状态，确认实际副本数。`);
      if (replicas !== undefined) {
        setStatus(null);
        setRequested(null);
      }
    } finally {
      setOperation(null);
    }
  }

  return (
    <div className="self-start space-y-4 rounded-xl border border-slate-200 bg-white p-4 text-sm">
      <div>
        <h3 className="font-medium text-slate-900">Matcher 调度实例</h3>
        <p className="mt-1 text-xs leading-relaxed text-slate-600">设置 Kubernetes 中运行多少个 Matcher Pod。</p>
      </div>
      <form className="space-y-1.5" onSubmit={(event) => {
        event.preventDefault();
        if (!busy && token.trim()) void updateReplicas();
      }}>
        <label htmlFor="matcher-scale-token" className="font-medium text-slate-700">扩缩容管理 token</label>
        <div className="flex flex-wrap gap-2">
          <input id="matcher-scale-token" type="password" autoComplete="off" value={token} disabled={busy}
            onChange={(event) => {
              setToken(event.target.value);
              setStatus(null);
              setRequested(null);
              setError("");
            }} className="min-w-28 flex-1 rounded border border-slate-300 px-2 py-1.5" />
          <button type="submit" disabled={busy || !token.trim()}
            className="rounded border border-slate-300 px-3 py-1.5 disabled:opacity-40">
            {operation === "read" ? "查询中…" : status ? "刷新状态" : "查询实例状态"}
          </button>
        </div>
        {!status && <p className="text-xs text-slate-500">使用后端配置的管理 token 查询或调整实例数。</p>}
      </form>
      {status && (
        <>
          <div className="space-y-2 rounded-lg bg-slate-50 p-3" aria-live="polite">
            <dl className="grid grid-cols-2 gap-3">
              <div>
                <dt className="text-xs text-slate-600">{requested === null ? "Kubernetes 目标" : "已提交的新目标"}</dt>
                <dd className="text-xl font-semibold tabular-nums text-slate-900">{configuredTarget} <span className="text-xs font-normal">个</span></dd>
              </div>
              <div>
                <dt className="text-xs text-slate-600">上次查询时已就绪</dt>
                <dd className="text-xl font-semibold tabular-nums text-slate-900">{status.ready} <span className="text-xs font-normal">个</span></dd>
              </div>
            </dl>
            <p className={atTarget ? "text-xs font-medium text-emerald-800" : "text-xs font-medium text-amber-800"}>
              {requested !== null ? "扩缩容请求已受理，刷新状态查看进度。" : atTarget ? "就绪实例数已达到目标。" : "就绪实例数与目标有差异，刷新状态查看进度。"}
            </p>
            <p className="text-xs text-slate-500">查询时间 {statusTimeFormat.format(status.checkedAt)}</p>
          </div>
          <div className="space-y-1.5">
            <label htmlFor="matcher-replica-count" className="font-medium text-slate-700">调整为多少个实例</label>
            <div className="flex flex-wrap items-center gap-2">
              <input id="matcher-replica-count" type="number" min={1} max={status.maxReplicas} step={1}
                value={target} disabled={busy} onChange={(event) => setTarget(event.target.value)}
                aria-describedby="matcher-replica-range" className="w-20 rounded border border-slate-300 px-2 py-1.5" />
              <button type="button" disabled={busy || !validTarget || count === configuredTarget}
                onClick={() => void updateReplicas(count)}
                className="rounded bg-slate-900 px-3 py-1.5 text-white disabled:opacity-40">
                {operation === "scale" ? "提交中…" : validTarget && count !== configuredTarget ? `调整为 ${count} 个实例` : "应用数量"}
              </button>
            </div>
            <p id="matcher-replica-range" className="text-xs text-slate-500">本环境允许设置 1–{status.maxReplicas} 个实例。</p>
          </div>
        </>
      )}
      {error && <p role="alert" className="text-red-700">{error}</p>}
      <div className="space-y-1.5 border-t border-slate-200 pt-3 text-xs leading-relaxed text-slate-600">
        <h4 className="text-sm font-medium text-slate-800">每个 Matcher Pod 负责什么？</h4>
        <ul className="list-disc space-y-1 pl-4">
          <li><span className="font-medium text-slate-800">订单调度</span> · 从共享 MongoDB 领取待处理订单、匹配骑手并创建邀请。</li>
          <li><span className="font-medium text-slate-800">实时投递</span> · 维护连到本 Pod 的骑手 WebSocket，并发送这些骑手的邀请。</li>
        </ul>
        <p>新增一个 Pod 就增加一套调度和投递能力。各 Pod 按空闲情况领单，共享订单状态。</p>
      </div>
    </div>
  );
}
