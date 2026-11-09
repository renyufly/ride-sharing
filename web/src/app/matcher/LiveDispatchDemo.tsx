"use client";

import { useEffect, useRef, useState } from "react";

type Pickup = { latitude: number; longitude: number };
type Offer = { orderId: number; requestId: string; pickup: Pickup };
type RiderState = { connected: boolean; offer: Offer | null; message: string };
type LiveResult = {
  InputCount: number;
  AcceptedCount: number;
  UnassignedCount: number;
  Attempts: Array<{
    OrderID: number;
    RiderUID: number;
    RequestID: string;
    Status: string;
  }>;
  Assignments: Array<{ OrderID: number; RiderUID: number }>;
};

const RIDERS = [
  { uid: 101, location: { latitude: 37.775, longitude: -122.4195 } },
  { uid: 102, location: { latitude: 37.776, longitude: -122.4205 } },
] as const;

const INITIAL_RIDERS: Record<number, RiderState> = {
  101: { connected: false, offer: null, message: "未连接" },
  102: { connected: false, offer: null, message: "未连接" },
};

function socketUrl(uid: number): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const origin =
    process.env.NEXT_PUBLIC_MATCHER_WS_URL ||
    `${scheme}//${window.location.hostname}:8082`;
  const url = new URL("/matcher/ws/rider", origin);
  url.searchParams.set("riderUid", String(uid));
  return url.toString();
}

export function LiveDispatchDemo() {
  const sockets = useRef(new Map<number, WebSocket>());
  const [riders, setRiders] = useState(INITIAL_RIDERS);
  const [pickup, setPickup] = useState<Pickup>({
    latitude: 37.7749,
    longitude: -122.4194,
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [result, setResult] = useState<LiveResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const activeSockets = sockets.current;
    return () => {
      for (const socket of activeSockets.values()) socket.close();
      activeSockets.clear();
    };
  }, []);

  function updateRider(uid: number, change: Partial<RiderState>) {
    setRiders((current) => ({
      ...current,
      [uid]: { ...current[uid], ...change },
    }));
  }

  function connectRider(uid: number, location: Pickup) {
    const existing = sockets.current.get(uid);
    if (existing && existing.readyState !== WebSocket.CLOSED) return;

    const socket = new WebSocket(socketUrl(uid));
    sockets.current.set(uid, socket);
    updateRider(uid, { message: "连接中…" });

    socket.onopen = () => {
      socket.send(
        JSON.stringify({ type: "matcher.rider.location", data: { location } }),
      );
      updateRider(uid, { connected: true, message: "在线，位置已上报" });
    };

    socket.onmessage = (event: MessageEvent<string>) => {
      try {
        const message = JSON.parse(event.data) as {
          type: string;
          data?: Offer & { processed?: boolean };
        };
        if (message.type === "matcher.order.request" && message.data) {
          updateRider(uid, { offer: message.data, message: "收到派单请求" });
        } else if (message.type === "matcher.order.response_ack") {
          updateRider(uid, {
            message: message.data?.processed
              ? "应答已收到，等待派单结果"
              : "请求已失效",
          });
        }
      } catch {
        updateRider(uid, { message: "收到无法解析的消息" });
      }
    };

    socket.onerror = () => updateRider(uid, { message: "连接失败" });
    socket.onclose = () => {
      if (sockets.current.get(uid) !== socket) return;
      sockets.current.delete(uid);
      updateRider(uid, { connected: false, offer: null, message: "已断开" });
    };
  }

  function respond(uid: number, accepted: boolean) {
    const socket = sockets.current.get(uid);
    const offer = riders[uid].offer;
    if (!offer || !socket || socket.readyState !== WebSocket.OPEN) return;

    socket.send(
      JSON.stringify({
        type: accepted ? "matcher.order.accept" : "matcher.order.decline",
        data: { orderId: offer.orderId, requestId: offer.requestId },
      }),
    );
    updateRider(uid, { offer: null, message: accepted ? "已接单" : "已拒单" });
  }

  async function submitOrder() {
    setError(null);
    setResult(null);
    setIsSubmitting(true);
    try {
      const response = await fetch("/matcher-api/matcher/live/order", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          orderId: Date.now(),
          pickup,
          topK: 2,
          maxExtraDistanceMeters: 2000,
        }),
      });
      const data: LiveResult | { error: string } = await response.json();
      if (!response.ok) {
        throw new Error("error" in data ? data.error : `HTTP ${response.status}`);
      }
      setResult(data as LiveResult);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <section className="max-w-7xl mx-auto px-4 pt-8 md:px-8 space-y-4">
      <div>
        <h2 className="text-xl font-semibold text-slate-900">实时派单演示</h2>
        <p className="text-sm text-slate-500">
          先连接两名骑手，再提交订单。拒绝第一位骑手的请求，可以观察重新派单。
        </p>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        {RIDERS.map(({ uid, location }) => (
          <div key={uid} className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h3 className="font-medium text-slate-800">骑手 {uid}</h3>
                <p className="text-sm text-slate-500">{riders[uid].message}</p>
              </div>
              <button
                type="button"
                onClick={() => connectRider(uid, location)}
                disabled={riders[uid].connected}
                className="rounded-md bg-blue-600 px-3 py-2 text-sm text-white disabled:bg-slate-300"
              >
                连接
              </button>
            </div>
            {riders[uid].offer && (
              <div className="rounded-md bg-amber-50 p-3 text-sm space-y-2">
                <p>订单 {riders[uid].offer?.orderId} 等待应答</p>
                <div className="flex gap-2">
                  <button type="button" onClick={() => respond(uid, true)} className="rounded bg-emerald-600 px-3 py-1.5 text-white">接单</button>
                  <button type="button" onClick={() => respond(uid, false)} className="rounded bg-rose-600 px-3 py-1.5 text-white">拒单</button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
        <h3 className="font-medium text-slate-800">提交一笔订单</h3>
        <div className="flex flex-wrap gap-3">
          <label className="text-sm text-slate-600">纬度
            <input type="number" step="any" value={pickup.latitude} onChange={(event) => setPickup((value) => ({ ...value, latitude: Number(event.target.value) }))} className="ml-2 w-32 rounded border border-slate-300 px-2 py-1" />
          </label>
          <label className="text-sm text-slate-600">经度
            <input type="number" step="any" value={pickup.longitude} onChange={(event) => setPickup((value) => ({ ...value, longitude: Number(event.target.value) }))} className="ml-2 w-32 rounded border border-slate-300 px-2 py-1" />
          </label>
        </div>
        <button
          type="button"
          onClick={submitOrder}
          disabled={isSubmitting || !RIDERS.every(({ uid }) => riders[uid].connected)}
          className="rounded-md bg-blue-600 px-4 py-2 text-sm text-white disabled:bg-slate-300"
        >
          {isSubmitting ? "等待骑手应答…" : "提交订单"}
        </button>
        {error && <p role="alert" className="text-sm text-rose-600">{error}</p>}
        {result && (
          <div className="text-sm text-slate-700 space-y-2">
            <p>{result.AcceptedCount ? `已分配给骑手 ${result.Assignments[0]?.RiderUID}` : "没有骑手接单"}</p>
            <ol className="list-decimal pl-5">
              {result.Attempts.map((attempt) => (
                <li key={attempt.RequestID}>骑手 {attempt.RiderUID}：{attempt.Status}</li>
              ))}
            </ol>
          </div>
        )}
      </div>
    </section>
  );
}
