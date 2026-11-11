"use client";

import { useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import type { MapOrder } from "./LiveDispatchMap";
import { MatcherReplicaControls } from "./MatcherReplicaControls";
import { OrderDispatchTrace, type OrderTraceEvent } from "./OrderDispatchTrace";

const LiveDispatchMap = dynamic(() => import("./LiveDispatchMap").then((module) => module.LiveDispatchMap), { ssr: false });

type Pickup = { latitude: number; longitude: number };
type Offer = { orderId: number; requestId: string; pickup: Pickup; receivedAt: number };
type RiderState = { connected: boolean; offers: Offer[]; message: string };
type RiderOrder = { status: "assigned" | "completing" | "completed"; error?: string };
type RiderOrders = Record<number, Record<number, RiderOrder>>;
type LiveResult = {
  InputCount: number;
  AcceptedCount: number;
  UnassignedCount: number;
  Attempts: Array<{
    OrderID: number;
    RiderUID: number;
    RequestID: string;
    Status: string;
  }> | null;
  Assignments: Array<{ OrderID: number; RiderUID: number }> | null;
  orders?: Array<{ ID: number; Pickup: { Latitude: number; Longitude: number } }> | null;
  pipeline?: {
    candidateWorkers: number;
    offerWorkers: number;
    batchSize: number;
    batchPoolCapacity: number;
    candidatePoolCapacity: number;
    topK: number;
    candidateWorkerCompleted: number[] | null;
    maxCandidateQueueDepth: number;
    maxReorderDepth: number;
  };
};

type DispatchObservation = {
  orderId: number;
  source: "HTTP" | "WebSocket" | "页面";
  riderUid?: number;
  requestId?: string;
};
type Activity = { id: number; time: string; text: string; observation?: DispatchObservation };
type SocketMessage = { type: string; data?: {
  orderId?: number; requestId?: string; pickup?: Pickup;
  processed?: boolean; status?: string; error?: string;
} };

type PlacedRider = { uid: number; location: Pickup };
type RunKind = "single" | "batch";
const BOUNDS = { minLatitude: 37.70, maxLatitude: 37.82, minLongitude: -122.52, maxLongitude: -122.35 };

const RESPONSE_WINDOW_MS = 15_000;
const SCHEDULED_ORDERS_URL = "/matcher-api/matcher/v2/live/orders";
const STATUS_POLL_MS = 750;
const STATUS_TIMEOUT_MS = 135_000;
const scheduledStatusLabels = {
  queued: "排队中",
  matching: "正在匹配骑手",
  offered: "等待骑手回复",
  assigned: "骑手已接单，最终分配",
  unassigned: "未能分配骑手",
  completed: "订单已完成",
};
type ScheduledStatus = keyof typeof scheduledStatusLabels;
type ScheduledOrder = {
  status: ScheduledStatus;
  receivedPod?: string;
  createdAt?: string;
  trace?: OrderTraceEvent[];
};

function isDispatchFinished(status: ScheduledStatus | null): boolean {
  return status === "assigned" || status === "unassigned" || status === "completed";
}

async function readScheduledOrder(response: Response, orderId: number): Promise<ScheduledOrder> {
  const data: (Partial<ScheduledOrder> & { orderId?: string; error?: string }) | null = await response.json().catch(() => null);
  if (!response.ok) throw new Error(data?.error ?? `HTTP ${response.status}`);
  const status = data?.status;
  if (data?.orderId !== String(orderId) || !status || !Object.hasOwn(scheduledStatusLabels, status)) {
    throw new Error("异步订单接口返回了无效的订单状态");
  }
  return { status, receivedPod: data.receivedPod, createdAt: data.createdAt, trace: data.trace };
}

function waitForStatusPoll(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    signal.throwIfAborted();
    const timer = window.setTimeout(() => {
      signal.removeEventListener("abort", cancel);
      resolve();
    }, STATUS_POLL_MS);
    function cancel() {
      window.clearTimeout(timer);
      reject(signal.reason);
    }
    signal.addEventListener("abort", cancel, { once: true });
  });
}

function attemptLabel(status: string): string {
  switch (status) {
    case "accepted": return "接单成功，已提交";
    case "declined": return "拒单，释放预占并重派";
    case "timed_out": return "超时，释放预占并重派";
    case "delivery_failed": return "投递失败，释放预占并重派";
    default: return status;
  }
}

function newOrderId(): number {
  const parts = new Uint32Array(2);
  window.crypto.getRandomValues(parts);
  return ((parts[0] & 0x1fffff) * 0x100000000 + parts[1]) || 1;
}

function socketUrl(uid: number, kind: RunKind): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const origin =
    process.env.NEXT_PUBLIC_MATCHER_WS_URL ||
    `${scheme}//${window.location.hostname}:8082`;
  const path = kind === "single" ? "/matcher/v2/ws/rider" : "/matcher/ws/rider";
  const url = new URL(path, origin);
  url.searchParams.set("riderUid", String(uid));
  return url.toString();
}

export function LiveDispatchDemo() {
  const sockets = useRef(new Map<number, WebSocket>());
  const knownOrders = useRef(new Map<number, Set<number>>());
  const sentResponses = useRef(new Map<string, "accept" | "decline">());
  const confirmedRequests = useRef(new Set<string>());
  const submission = useRef<AbortController | null>(null);
  const trackedOrder = useRef<{ id: number; status: ScheduledStatus | null } | null>(null);
  const traceRefreshId = useRef(0);
  const nextActivityId = useRef(0);
  const nextRiderUid = useRef(101);
  const [riders, setRiders] = useState<Record<number, RiderState>>({});
  const [riderOrders, setRiderOrders] = useState<Record<RunKind, RiderOrders>>({ single: {}, batch: {} });
  const [placedRiders, setPlacedRiders] = useState<PlacedRider[]>([]);
  const [targetCount, setTargetCount] = useState(6);
  const [placementEnabled, setPlacementEnabled] = useState(true);
  const [orders, setOrders] = useState<MapOrder[]>([]);
  const [selectedOrderId, setSelectedOrderId] = useState<number | null>(null);
  const [topK, setTopK] = useState(3);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [asyncOrderId, setAsyncOrderId] = useState<number | null>(null);
  const [orderDetails, setOrderDetails] = useState<ScheduledOrder | null>(null);
  const [traceError, setTraceError] = useState("");
  const [refreshingTrace, setRefreshingTrace] = useState(false);
  const [clock, setClock] = useState(Date.now());
  const [runKind, setRunKind] = useState<RunKind>("batch");
  const [batchCount, setBatchCount] = useState(4);
  const [batchSize, setBatchSize] = useState(2);
  const [capacity, setCapacity] = useState(0);
  const [pickup, setPickup] = useState<Pickup>({
    latitude: 37.7749,
    longitude: -122.4194,
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [result, setResult] = useState<LiveResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const placementLocked = isSubmitting && runKind === "batch";

  useEffect(() => {
    const activeSockets = sockets.current;
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => {
      window.clearInterval(timer);
      const activeSubmission = submission.current;
      submission.current = null;
      activeSubmission?.abort();
      trackedOrder.current = null;
      for (const socket of activeSockets.values()) socket.close();
      activeSockets.clear();
    };
  }, []);

  function addActivity(text: string, observation?: DispatchObservation) {
    const item = {
      id: ++nextActivityId.current,
      time: new Date().toLocaleTimeString(undefined, {
        hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3,
      }),
      text,
      observation,
    };
    setActivity((current) => [...current.slice(-39), item]);
  }

  function rememberOrder(uid: number, orderId: number) {
    const orders = knownOrders.current.get(uid) ?? new Set<number>();
    orders.add(orderId);
    if (orders.size > 20) orders.delete(orders.values().next().value!);
    knownOrders.current.set(uid, orders);
  }

  function updateRider(uid: number, change: Partial<RiderState>) {
    setRiders((current) => ({
      ...current,
      [uid]: { ...current[uid], ...change },
    }));
  }

  function setRiderOrder(kind: RunKind, uid: number, orderId: number, next: RiderOrder) {
    setRiderOrders((current) => {
      const orders = current[kind][uid];
      const previous = orders?.[orderId];
      if (previous?.status === "completed" ||
          (previous?.status === next.status && previous?.error === next.error)) return current;
      return {
        ...current,
        [kind]: { ...current[kind], [uid]: { ...orders, [orderId]: next } },
      };
    });
  }

  function addRider(location: Pickup) {
    if (placedRiders.length >= targetCount || placementLocked) return;
    if (location.latitude < BOUNDS.minLatitude || location.latitude > BOUNDS.maxLatitude ||
        location.longitude < BOUNDS.minLongitude || location.longitude > BOUNDS.maxLongitude) {
      setError("请在旧金山演示区域内放置骑手");
      return;
    }
    const uid = nextRiderUid.current++;
    setPlacedRiders((current) => [...current, { uid, location }]);
    setRiders((current) => ({ ...current, [uid]: { connected: false, offers: [], message: "未连接" } }));
    setError(null);
  }

  function generateRemaining() {
    if (placementLocked) return;
    const count = Math.max(0, targetCount - placedRiders.length);
    const generated: PlacedRider[] = Array.from({ length: count }, () => ({
      uid: nextRiderUid.current++,
      location: {
        latitude: 37.715 + Math.random() * 0.09,
        longitude: -122.505 + Math.random() * 0.14,
      },
    }));
    setPlacedRiders((current) => [...current, ...generated]);
    setRiders((current) => ({ ...current, ...Object.fromEntries(generated.map(({ uid }) => [uid, { connected: false, offers: [], message: "未连接" }])) }));
    setPlacementEnabled(false);
    addActivity(`已生成 ${generated.length} 位骑手；请逐个连接需要参与匹配的骑手`);
  }

  function disconnectRider(uid: number) {
    const socket = sockets.current.get(uid);
    if (!socket) return;
    updateRider(uid, { message: "正在主动退出连接…" });
    socket.close();
  }

  function clearConnections() {
    for (const socket of sockets.current.values()) socket.close();
    sockets.current.clear();
    knownOrders.current.clear();
    sentResponses.current.clear();
    confirmedRequests.current.clear();
    trackedOrder.current = null;
    setAsyncOrderId(null);
    setOrderDetails(null);
    setTraceError("");
    setRefreshingTrace(false);
    setOrders([]);
    setSelectedOrderId(null);
    setResult(null);
    setActivity([]);
    setError(null);
  }

  function resetRiders() {
    if (submission.current) return;
    clearConnections();
    setPlacedRiders([]);
    setRiders({});
    setRiderOrders({ single: {}, batch: {} });
    setPlacementEnabled(true);
  }

  function changeRunKind(kind: RunKind) {
    if (kind === runKind || submission.current) return;
    const reconnect = placedRiders.filter(({ uid }) => {
      const state = sockets.current.get(uid)?.readyState;
      return state === WebSocket.OPEN || state === WebSocket.CONNECTING;
    });
    clearConnections();
    setRunKind(kind);
    setRiders(Object.fromEntries(placedRiders.map(({ uid }) => [uid, {
      connected: false, offers: [], message: "未连接",
    }])));
    for (const { uid, location } of reconnect) connectRider(uid, location, kind);
  }

  function connectRider(uid: number, location: Pickup, kind: RunKind = runKind) {
    const existing = sockets.current.get(uid);
    if (existing && existing.readyState !== WebSocket.CLOSED) return;

    const socket = new WebSocket(socketUrl(uid, kind));
    const seenOffers = new Set<string>();
    sockets.current.set(uid, socket);
    updateRider(uid, { message: "连接中…" });

    socket.onopen = () => {
      if (sockets.current.get(uid) !== socket) return;
      socket.send(
        JSON.stringify({ type: "matcher.rider.location", data: { location } }),
      );
      const orderIds = new Set(knownOrders.current.get(uid));
      for (const [id, order] of Object.entries(riderOrders[kind][uid] ?? {})) {
        if (order.status !== "completed") orderIds.add(Number(id));
      }
      for (const orderId of orderIds) {
        socket.send(JSON.stringify({ type: "matcher.order.status_query", data: { orderId } }));
      }
      updateRider(uid, { connected: true, message: "在线，位置已发送" });
      addActivity(`骑手 ${uid} 已建立 WebSocket 长连接`);
    };

    socket.onmessage = (event: MessageEvent<string>) => {
      if (sockets.current.get(uid) !== socket) return;
      try {
        const message = JSON.parse(event.data) as SocketMessage;
        const data = message.data;
        if (message.type === "matcher.order.request" && data?.orderId && data.requestId && data.pickup) {
          const tracked = trackedOrder.current;
          if (kind === "single") {
            if (seenOffers.has(data.requestId) ||
                (tracked?.id === data.orderId && isDispatchFinished(tracked.status))) return;
            seenOffers.add(data.requestId);
          }
          const offer: Offer = {
            orderId: data.orderId,
            requestId: data.requestId,
            pickup: data.pickup,
            receivedAt: Date.now(),
          };
          rememberOrder(uid, offer.orderId);
          if (tracked?.id !== offer.orderId) {
            setOrders((current) => current.some((item) => item.orderId === offer.orderId)
              ? current.map((item) => item.orderId === offer.orderId ? { ...item, status: `邀请骑手 ${uid}，等待回复` } : item)
              : [...current, { orderId: offer.orderId, pickup: offer.pickup, status: `邀请骑手 ${uid}，等待回复` }]);
          }
          setSelectedOrderId(offer.orderId);
          setRiders((current) => ({
            ...current,
            [uid]: {
              ...current[uid],
              offers: [...current[uid].offers.filter((item) => item.requestId !== offer.requestId), offer],
              message: "收到派单邀请，等待选择",
            },
          }));
          addActivity(`订单 ${offer.orderId} → 骑手 ${uid}：邀请已到达`, {
            orderId: offer.orderId, source: "WebSocket", riderUid: uid, requestId: offer.requestId,
          });
        } else if (message.type === "matcher.order.response_ack" && data?.requestId) {
          const action = sentResponses.current.get(data.requestId);
          sentResponses.current.delete(data.requestId);
          const label = data.processed
            ? action === "decline" ? "拒单已记录，等待重派" : "接单回复已收到，等待最终提交"
            : "邀请已失效";
          if (!confirmedRequests.current.has(data.requestId)) {
            updateRider(uid, { message: label });
          }
          addActivity(`骑手 ${uid}：${label}`, data.orderId ? {
            orderId: data.orderId, source: "WebSocket", riderUid: uid, requestId: data.requestId,
          } : undefined);
        } else if (message.type === "matcher.order.assigned" && data?.orderId) {
          rememberOrder(uid, data.orderId);
          setRiderOrder(kind, uid, data.orderId, { status: "assigned" });
          if (data.requestId) confirmedRequests.current.add(data.requestId);
          setRiders((current) => ({
            ...current,
            [uid]: {
              ...current[uid],
              offers: current[uid].offers.filter((item) => item.requestId !== data.requestId),
              message: `订单 ${data.orderId} 已最终分配`,
            },
          }));
          addActivity(`订单 ${data.orderId} → 骑手 ${uid}：账本提交成功`, {
            orderId: data.orderId, source: "WebSocket", riderUid: uid, requestId: data.requestId,
          });
          if (trackedOrder.current?.id !== data.orderId) {
            setOrders((current) => current.map((item) => item.orderId === data.orderId ? { ...item, status: `骑手 ${uid} 已接单，最终分配` } : item));
          }
        } else if (message.type === "matcher.order.status" && data?.orderId && data.status) {
          const orderId = data.orderId;
          if (data.error) {
            setRiderOrder(kind, uid, orderId, { status: "assigned", error: data.error });
            addActivity(`订单 ${orderId}：${data.error}`, { orderId, source: "WebSocket", riderUid: uid });
            return;
          }
          if (data.status === "assigned" || data.status === "completed") {
            setRiderOrder(kind, uid, orderId, { status: data.status });
          }
          if (data.status === "completed") {
            if (trackedOrder.current?.id === orderId) {
              trackedOrder.current = { id: orderId, status: "completed" };
              void refreshOrderTrace(orderId);
            }
            setOrders((current) => current.map((item) => item.orderId === orderId
              ? { ...item, status: scheduledStatusLabels.completed }
              : item));
          }
          const label = data.status === "assigned" ? "已最终分配"
            : data.status === "completed" ? "订单已完成"
            : data.status === "pending" ? "派单处理中" : "未分配给该骑手";
          updateRider(uid, { message: `订单 ${orderId}：${label}` });
          addActivity(`骑手 ${uid} · 订单 ${orderId}：${label}`, { orderId, source: "WebSocket", riderUid: uid });
        }
      } catch {
        updateRider(uid, { message: "收到无法解析的消息" });
      }
    };

    socket.onerror = () => {
      if (sockets.current.get(uid) === socket) updateRider(uid, { message: "连接失败" });
    };
    socket.onclose = () => {
      if (sockets.current.get(uid) !== socket) return;
      sockets.current.delete(uid);
      updateRider(uid, { connected: false, offers: [], message: "已断开，可重新连接查询订单状态" });
      addActivity(`骑手 ${uid} 的 WebSocket 已断开`);
    };
  }

  function respond(uid: number, offer: Offer, accepted: boolean) {
    const socket = sockets.current.get(uid);
    if (!socket || socket.readyState !== WebSocket.OPEN) return;

    sentResponses.current.set(offer.requestId, accepted ? "accept" : "decline");
    socket.send(
      JSON.stringify({
        type: accepted ? "matcher.order.accept" : "matcher.order.decline",
        data: { orderId: offer.orderId, requestId: offer.requestId },
      }),
    );
    setRiders((current) => ({
      ...current,
      [uid]: {
        ...current[uid],
        offers: current[uid].offers.filter((item) => item.requestId !== offer.requestId),
        message: accepted ? "接单回复已发送，等待服务端确认" : "拒单回复已发送",
      },
    }));
    if (trackedOrder.current?.id !== offer.orderId) {
      setOrders((current) => current.map((item) => item.orderId === offer.orderId ? { ...item, status: accepted ? `骑手 ${uid} 已回复接单，等待提交` : `骑手 ${uid} 已拒单，等待重派` } : item));
    }
    addActivity(`订单 ${offer.orderId}：骑手 ${uid} ${accepted ? "发送接单回复" : "发送拒单回复"}`, {
      orderId: offer.orderId, source: "页面", riderUid: uid, requestId: offer.requestId,
    });
  }

  function completeRiderOrder(uid: number, orderId: number) {
    const socket = sockets.current.get(uid);
    if (socket?.readyState !== WebSocket.OPEN ||
        riderOrders[runKind][uid]?.[orderId]?.status !== "assigned") return;

    try {
      socket.send(JSON.stringify({ type: "matcher.order.complete", data: { orderId } }));
      setRiderOrder(runKind, uid, orderId, { status: "completing" });
      addActivity(`骑手 ${uid} 已提交完成请求`, { orderId, source: "页面", riderUid: uid });
    } catch {
      setRiderOrder(runKind, uid, orderId, { status: "assigned", error: "发送失败，请重新连接后重试" });
    }
  }

  async function refreshOrderTrace(orderId: number) {
    const refreshId = ++traceRefreshId.current;
    const isCurrent = () => trackedOrder.current?.id === orderId && traceRefreshId.current === refreshId;
    setRefreshingTrace(true);
    setTraceError("");
    try {
      const response = await fetch(`${SCHEDULED_ORDERS_URL}/${orderId}`, {
        cache: "no-store",
        signal: AbortSignal.timeout(10_000),
      });
      const details = await readScheduledOrder(response, orderId);
      if (isCurrent()) setOrderDetails(details);
    } catch (cause) {
      if (isCurrent()) setTraceError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      if (isCurrent()) setRefreshingTrace(false);
    }
  }

  async function submitScheduledOrder(orderId: number, request: RequestInit, signal: AbortSignal) {
    let response = await fetch(SCHEDULED_ORDERS_URL, request).catch(() => null);
    if (!response || response.status >= 500) {
      // Retry the same ID so a lost POST response cannot create a second order.
      await waitForStatusPoll(signal);
      response = await fetch(SCHEDULED_ORDERS_URL, request);
    }
    let { status } = await readScheduledOrder(response, orderId);
    signal.throwIfAborted();
    addActivity(`下单接口已响应（HTTP ${response.status}）`, { orderId, source: "HTTP" });

    for (;;) {
      signal.throwIfAborted();
      if (trackedOrder.current?.status === "completed") return;
      if (trackedOrder.current?.status !== status) {
        trackedOrder.current = { id: orderId, status };
        const label = scheduledStatusLabels[status];
        setOrders((current) => current.map((item) => item.orderId === orderId ? { ...item, status: label } : item));
        addActivity(`订单 ${orderId}：${label}`, { orderId, source: "HTTP" });
      }
      if (isDispatchFinished(status)) {
        setRiders((current) => Object.fromEntries(Object.entries(current).map(([uid, rider]) => [uid, {
          ...rider,
          offers: rider.offers.filter((offer) => offer.orderId !== orderId),
        }])));
        void refreshOrderTrace(orderId);
        return;
      }

      await waitForStatusPoll(signal);
      const next = await fetch(`${SCHEDULED_ORDERS_URL}/${orderId}`, { signal, cache: "no-store" }).catch(() => null);
      signal.throwIfAborted();
      if (!next || next.status === 429 || next.status >= 500) {
        setError(`暂时无法查询订单 ${orderId}，正在重试。`);
        continue;
      }
      const details = await readScheduledOrder(next, orderId);
      signal.throwIfAborted();
      if (trackedOrder.current?.status === "completed") return;
      status = details.status;
      setOrderDetails(details);
      setError(null);
    }
  }

  async function submitOrder() {
    const onlineCount = placedRiders.filter(({ uid }) => riders[uid]?.connected).length;
    if (submission.current || (runKind === "batch" && onlineCount === 0)) return;

    const controller = new AbortController();
    submission.current = controller;
    const orderId = runKind === "single" ? newOrderId() : null;
    const effectiveTopK = Math.min(topK, orderId === null ? onlineCount : targetCount);
    const timeout = orderId === null ? undefined : window.setTimeout(() => {
      controller.abort(new Error(`等待订单 ${orderId} 状态超时。`));
    }, STATUS_TIMEOUT_MS);
    setError(null);
    setResult(null);
    setActivity([]);
    setOrders([]);
    setSelectedOrderId(null);
    setIsSubmitting(true);
    const body = orderId !== null
      ? { orderId, pickup, topK: effectiveTopK, maxExtraDistanceMeters: 2000 }
      : { orderCount: batchCount, batchSize, seed: newOrderId(), topK: effectiveTopK, maxExtraDistanceMeters: 2000, maxOrdersPerRider: capacity };
    const request = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: controller.signal,
    };
    if (orderId !== null) {
      trackedOrder.current = { id: orderId, status: null };
      setAsyncOrderId(orderId);
      setOrderDetails(null);
      setTraceError("");
      setRefreshingTrace(false);
      setOrders([{ orderId, pickup, status: "正在提交" }]);
      setSelectedOrderId(orderId);
    }
    if (orderId !== null) {
      addActivity(`正在提交异步订单 ${orderId}`, { orderId, source: "页面" });
    } else {
      addActivity(`${batchCount} 笔生成订单已提交到实时流水线`);
    }
    try {
      if (orderId !== null) {
        await submitScheduledOrder(orderId, request, controller.signal);
        return;
      }

      const response = await fetch("/matcher-api/matcher/live/demo", request);
      const data: LiveResult | { error: string } = await response.json();
      controller.signal.throwIfAborted();
      if (!response.ok) {
        throw new Error("error" in data ? data.error : `HTTP ${response.status}`);
      }
      const liveResult = data as LiveResult;
      setResult(liveResult);
      for (const assignment of liveResult.Assignments ?? []) {
        setRiderOrder("batch", assignment.RiderUID, assignment.OrderID, { status: "assigned" });
      }
      setOrders((current) => {
        const byId = new Map(current.map((item) => [item.orderId, item]));
        for (const item of liveResult.orders ?? []) {
          byId.set(item.ID, { ...byId.get(item.ID), orderId: item.ID, pickup: { latitude: item.Pickup.Latitude, longitude: item.Pickup.Longitude } });
        }
        return [...byId.values()].map((item) => {
          const assignment = (liveResult.Assignments ?? []).find((entry) => entry.OrderID === item.orderId);
          const lastAttempt = (liveResult.Attempts ?? []).filter((entry) => entry.OrderID === item.orderId).at(-1);
          return { ...item, status: assignment ? `骑手 ${assignment.RiderUID} 已接单，最终分配` : lastAttempt ? `未分配；最后一次邀请${attemptLabel(lastAttempt.Status)}` : "未找到可派骑手" };
        });
      });
      setSelectedOrderId((current) => current ?? liveResult.orders?.[0]?.ID ?? null);
      const finishedRequests = new Set((liveResult.Attempts ?? []).map((attempt) => attempt.RequestID));
      setRiders((current) => Object.fromEntries(
        Object.entries(current).map(([uid, rider]) => [uid, {
          ...rider,
          offers: rider.offers.filter((offer) => !finishedRequests.has(offer.requestId)),
        }]),
      ) as Record<number, RiderState>);
      addActivity(`流水线结束：接单 ${liveResult.AcceptedCount} 笔，未分配 ${liveResult.UnassignedCount} 笔`);
    } catch (cause) {
      if (submission.current !== controller) return;
      if (orderId !== null && trackedOrder.current?.status === "completed") return;
      const failure = controller.signal.aborted ? controller.signal.reason : cause;
      const message = failure instanceof Error ? failure.message : String(failure);
      setError(message);
      addActivity(`实时入口失败：${message}`, orderId === null ? undefined : { orderId, source: "页面" });
      if (orderId !== null) {
        const label = trackedOrder.current?.status ? "状态查询中断" : "提交结果未确认";
        setOrders((current) => current.map((item) => item.orderId === orderId ? { ...item, status: label } : item));
      }
    } finally {
      window.clearTimeout(timeout);
      if (submission.current === controller) {
        submission.current = null;
        setIsSubmitting(false);
      }
    }
  }

  const onlineCount = placedRiders.filter(({ uid }) => riders[uid]?.connected).length;
  const maxTopK = runKind === "single" ? targetCount : Math.max(1, onlineCount);
  const attempts = result?.Attempts ?? [];
  const assignments = result?.Assignments ?? [];
  const orderActivity = activity.filter((item) => item.observation?.orderId === asyncOrderId);
  const flowSteps = [
    ["Producer", "生成订单流"],
    ["Batch 与对象池", "复用有界队列和缓冲对象"],
    ["Top-K 候选 worker", "并行搜索附近骑手"],
    ["协调器与账本", "按序选择并原子预占容量"],
    ["Offer worker", "有界并发发送 WebSocket 邀请、等待回复"],
    ["提交或重派", "接单才提交；拒单、超时、投递失败则重选"],
  ];

  return (
    <section className="max-w-7xl mx-auto px-4 py-8 md:px-8 space-y-6">
      <div>
        <h2 className="text-2xl font-semibold text-slate-900">实时入口与 WebSocket 派单</h2>
        <p className="text-sm text-slate-500">
          单笔订单异步调度，多笔模式生成订单流。两种模式都通过骑手长连接发送邀请。
        </p>
        <a href="#live-order-controls" className="mt-2 inline-block text-sm text-blue-700 underline">骑手就绪后，跳转到开始订单流</a>
      </div>

      {runKind === "single" ? (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
          <MatcherReplicaControls />
          <div className="min-w-0 space-y-3 rounded-xl border border-slate-200 bg-white p-4">
            <h3 className="font-medium text-slate-900">单笔订单调度记录</h3>
            {asyncOrderId === null ? (
              <p className="text-sm text-slate-500">提交单笔异步订单后显示事件。</p>
            ) : (
              <>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="break-all text-sm font-medium text-slate-800">订单 #{asyncOrderId}</p>
                  <button type="button" disabled={isSubmitting || refreshingTrace}
                    onClick={() => void refreshOrderTrace(asyncOrderId)}
                    className="rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-40">
                    {refreshingTrace ? "正在刷新…" : "刷新调度记录"}
                  </button>
                </div>
                {orderDetails && <p className="text-sm text-slate-600">{scheduledStatusLabels[orderDetails.status]}</p>}
                {traceError && <p role="alert" className="text-sm text-red-700">调度记录查询失败（{traceError}）</p>}
                <OrderDispatchTrace receivedPod={orderDetails?.receivedPod} createdAt={orderDetails?.createdAt} trace={orderDetails?.trace} />
                <details className="text-sm text-slate-600">
                  <summary className="cursor-pointer">浏览器回执（{orderActivity.length} 条）</summary>
                  <ol className="mt-2 max-h-48 list-none space-y-2 overflow-y-auto" aria-label="浏览器回执">
                    {orderActivity.map(({ id, time, text, observation }) => (
                      <li key={id} className="border-l-2 border-blue-200 pl-3 text-sm text-slate-800">
                        <p><time className="mr-2 font-mono text-xs text-slate-500">{time}</time><span className="mr-2 text-xs font-medium text-blue-700">{observation?.source}</span>{text}</p>
                        {observation && (observation.riderUid !== undefined || observation.requestId) && (
                          <p className="flex flex-wrap gap-x-3 break-all font-mono text-xs text-slate-500">
                            {observation.riderUid !== undefined && <span>riderUid {observation.riderUid}</span>}
                            {observation.requestId && <span>requestId {observation.requestId}</span>}
                          </p>
                        )}
                      </li>
                    ))}
                  </ol>
                </details>
              </>
            )}
          </div>
        </div>
      ) : null}

      {runKind === "batch" && (
      <div className="rounded-xl border border-blue-100 bg-blue-50/70 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="font-semibold text-slate-900">多笔订单流处理路径</h3>
          <span className="text-xs text-blue-800">代码路径说明；实际记录见运行结果</span>
        </div>
        <div className="mt-4 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {flowSteps.map(([title, detail], index) => (
            <div key={title} className="rounded-lg border border-blue-100 bg-white p-3">
              <p className="text-sm font-semibold text-slate-800"><span className="mr-2 text-blue-600">{index + 1}.</span>{title}</p>
              <p className="mt-1 text-xs text-slate-500">{detail}</p>
            </div>
          ))}
        </div>
      </div>
      )}

      <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
        <div className="space-y-3">
          <div className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
            <h3 className="font-medium text-slate-800">在地图上布置骑手</h3>
            <div className="flex flex-wrap items-center gap-3 text-sm">
              <label>骑手总数
                <input type="number" min={1} max={20} value={targetCount} disabled={placementLocked} onChange={(event) => setTargetCount(Math.max(placedRiders.length, Math.min(20, Math.max(1, Math.trunc(Number(event.target.value)) || 1))))} className="ml-2 w-16 rounded border border-slate-300 px-2 py-1" />
              </label>
              <span>已布置 {placedRiders.length} / {targetCount}</span>
              <button type="button" onClick={() => setPlacementEnabled((value) => !value)} disabled={placementLocked || placedRiders.length >= targetCount} className="rounded bg-slate-100 px-3 py-1.5 text-slate-700 disabled:opacity-40">{placementEnabled ? "结束地图选点" : "继续地图选点"}</button>
              <button type="button" onClick={generateRemaining} disabled={placementLocked || placedRiders.length >= targetCount} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-40">自动补足剩余骑手</button>
              <button type="button" onClick={resetRiders} disabled={isSubmitting || placedRiders.length === 0} className="rounded border border-slate-300 px-3 py-1.5 disabled:opacity-40">重新布置</button>
            </div>
            <p className="text-xs text-slate-500">{placementEnabled && placedRiders.length < targetCount ? "点击地图任意位置手动添加骑手，再自动补足剩余数量。" : "地图选点已暂停。可逐个连接或主动退出骑手。"} 演示区域与服务端旧金山坐标范围一致。</p>
          </div>
          <LiveDispatchMap
            riders={placedRiders.map(({ uid, location }) => ({ uid, location, connected: riders[uid]?.connected ?? false, hasOffer: (riders[uid]?.offers.length ?? 0) > 0 }))}
            orders={orders}
            attempts={attempts}
            selectedOrderId={selectedOrderId}
            topK={Math.min(topK, maxTopK, onlineCount)}
            extraMeters={2000}
            placementEnabled={placementEnabled && placedRiders.length < targetCount && !placementLocked}
            onPlace={addRider}
            onSelectOrder={setSelectedOrderId}
          />
          {orders.length > 0 && <div className="flex flex-wrap gap-2 rounded-xl border border-slate-200 bg-white p-3 text-xs">
            <span className="py-1 text-slate-500">订单状态：</span>
            {orders.map((order) => <button key={order.orderId} type="button" onClick={() => setSelectedOrderId(order.orderId)} className={`rounded px-2 py-1 text-left ${selectedOrderId === order.orderId ? "bg-orange-100 text-orange-800" : "bg-slate-100 text-slate-600"}`}>#{order.orderId}<span className="ml-2">{order.status ?? "搜索骑手中"}</span></button>)}
          </div>}
        </div>
        <div className="max-h-[700px] space-y-3 overflow-y-auto rounded-xl border border-slate-200 bg-slate-100/60 p-3">
          <h3 className="font-medium text-slate-800">骑手连接与派单回复</h3>
          {placedRiders.length === 0 && <p className="text-sm text-slate-500">先在地图放置骑手，或自动生成全部骑手。</p>}
          {placedRiders.map(({ uid, location }) => (
          <div key={uid} className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h3 className="font-medium text-slate-800">骑手 {uid}</h3>
                <p className="text-sm text-slate-500" aria-live="polite">{riders[uid].message}</p>
                <p className="text-xs text-slate-400">{location.latitude.toFixed(5)}, {location.longitude.toFixed(5)}</p>
              </div>
              {riders[uid]?.connected ? (
                <button type="button" onClick={() => disconnectRider(uid)} className="rounded-md bg-slate-700 px-3 py-2 text-sm text-white">主动退出</button>
              ) : (
                <button type="button" onClick={() => connectRider(uid, location)} className="rounded-md bg-blue-600 px-3 py-2 text-sm text-white">连接</button>
              )}
            </div>
            <div className="space-y-2">
              {riders[uid].offers.map((offer) => {
                const secondsLeft = Math.max(0, Math.ceil((offer.receivedAt + RESPONSE_WINDOW_MS - clock) / 1000));
                return (
                  <div key={offer.requestId} className="rounded-md border border-amber-200 bg-amber-50 p-3 text-sm space-y-2">
                    <p className="font-medium">订单 {offer.orderId} 等待应答</p>
                    <p className="text-xs text-amber-800">预计应答窗口剩余 {secondsLeft} 秒</p>
                    <div className="flex gap-2">
                      <button type="button" onClick={() => respond(uid, offer, true)} disabled={secondsLeft === 0 || !riders[uid].connected} className="rounded bg-emerald-600 px-3 py-1.5 text-white disabled:opacity-40">接单</button>
                      <button type="button" onClick={() => respond(uid, offer, false)} disabled={secondsLeft === 0 || !riders[uid].connected} className="rounded bg-rose-600 px-3 py-1.5 text-white disabled:opacity-40">拒单</button>
                    </div>
                  </div>
                );
              })}
            </div>
            {Object.keys(riderOrders[runKind][uid] ?? {}).length > 0 && (
              <div className="space-y-2 border-t border-slate-100 pt-3">
                <h4 className="text-sm font-medium text-slate-700">已接单订单</h4>
                {Object.entries(riderOrders[runKind][uid]).map(([id, order]) => (
                  <div key={id} className="rounded-md border border-slate-200 px-3 py-2 text-sm">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span>订单 {id}</span>
                      {order.status === "completed" ? (
                        <span className="font-medium text-emerald-700">已完成</span>
                      ) : (
                        <button
                          type="button"
                          aria-label={`完成订单 ${id}`}
                          onClick={() => completeRiderOrder(uid, Number(id))}
                          disabled={!riders[uid].connected || order.status === "completing" || (isSubmitting && runKind === "batch")}
                          className="rounded bg-emerald-600 px-3 py-1.5 text-white disabled:opacity-40"
                        >
                          {order.status === "completing" ? "确认中…" : "完成订单"}
                        </button>
                      )}
                    </div>
                    {order.error && <p role="alert" className="mt-1 text-xs text-rose-600">{order.error}</p>}
                  </div>
                ))}
              </div>
            )}
          </div>
          ))}
        </div>
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-4">
        <h3 className="font-medium text-slate-800">实时事件</h3>
        <p className="mt-1 text-xs text-slate-500">记录页面收到的 WebSocket 消息及提交动作；流水线内部指标以服务端响应为准。</p>
        <ol className="mt-3 max-h-56 space-y-2 overflow-y-auto text-sm" aria-live="polite">
          {activity.length === 0 && <li className="text-slate-400">连接骑手并提交订单后显示事件。</li>}
          {activity.map((item) => (
            <li key={item.id} className="flex gap-3 border-l-2 border-blue-100 pl-3">
              <time className="shrink-0 text-xs text-slate-400">{item.time}</time>
              <span className="text-slate-700">{item.text}</span>
            </li>
          ))}
        </ol>
      </div>

      <div id="live-order-controls" className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
        <h3 className="font-medium text-slate-800">选择实时入口</h3>
        <div role="group" aria-label="实时演示模式" className="flex flex-wrap gap-2">
          <button type="button" onClick={() => changeRunKind("single")} disabled={isSubmitting} className={`rounded px-3 py-2 text-sm ${runKind === "single" ? "bg-blue-600 text-white" : "bg-slate-100 text-slate-700"}`}>单笔异步订单</button>
          <button type="button" onClick={() => changeRunKind("batch")} disabled={isSubmitting} className={`rounded px-3 py-2 text-sm ${runKind === "batch" ? "bg-blue-600 text-white" : "bg-slate-100 text-slate-700"}`}>多笔订单流</button>
        </div>
        {runKind === "single" ? (
          <div className="flex flex-wrap gap-3">
            <label className="text-sm text-slate-600">纬度
              <input type="number" step="any" value={pickup.latitude} onChange={(event) => setPickup((value) => ({ ...value, latitude: Number(event.target.value) }))} className="ml-2 w-32 rounded border border-slate-300 px-2 py-1" />
            </label>
            <label className="text-sm text-slate-600">经度
              <input type="number" step="any" value={pickup.longitude} onChange={(event) => setPickup((value) => ({ ...value, longitude: Number(event.target.value) }))} className="ml-2 w-32 rounded border border-slate-300 px-2 py-1" />
            </label>
          </div>
        ) : (
          <div className="flex flex-wrap gap-3">
            <label className="text-sm text-slate-600">订单数
              <input type="number" min={2} max={8} value={batchCount} onChange={(event) => setBatchCount(Math.max(2, Math.min(8, Number(event.target.value) || 2)))} className="ml-2 w-20 rounded border border-slate-300 px-2 py-1" />
            </label>
            <label className="text-sm text-slate-600">Batch 大小
              <select value={batchSize} onChange={(event) => setBatchSize(Number(event.target.value))} className="ml-2 rounded border border-slate-300 px-2 py-1">
                <option value={1}>1</option><option value={2}>2</option><option value={4}>4</option>
              </select>
            </label>
            <label className="text-sm text-slate-600">单骑手容量
              <select value={capacity} onChange={(event) => setCapacity(Number(event.target.value))} className="ml-2 rounded border border-slate-300 px-2 py-1">
                <option value={0}>不限</option><option value={1}>1 单</option><option value={2}>2 单</option>
              </select>
            </label>
          </div>
        )}
        <label className="text-sm text-slate-600">Top-K 候选数
          <input type="number" min={1} max={maxTopK} value={Math.min(topK, maxTopK)} onChange={(event) => setTopK(Math.max(1, Math.min(maxTopK, Math.trunc(Number(event.target.value)) || 1)))} className="ml-2 w-16 rounded border border-slate-300 px-2 py-1" />
        </label>
        <p className="text-xs text-slate-500">连接至少两位骑手并将 Top-K 设为 2 或更高，可拒绝首位骑手或等待约 15 秒观察重派。超时、投递失败只在服务端实际发生后显示。</p>
        <p className="text-xs text-slate-500">{runKind === "single"
          ? "单笔订单可以先入队，骑手上线后参与匹配。接单结果写入共享账本，重连后可查询。"
          : "多笔演示的接单结果保存在当前服务进程。骑手容量会跨多次订单流累计，直到订单完成或服务重启。"}</p>
        <button
          type="button"
          onClick={submitOrder}
          disabled={isSubmitting || (runKind === "batch" && onlineCount === 0)}
          className="rounded-md bg-blue-600 px-4 py-2 text-sm text-white disabled:bg-slate-300"
        >
          {isSubmitting ? "实时派单进行中…" : runKind === "single" ? "提交单笔订单" : `开始 ${batchCount} 笔订单流`}
        </button>
        {error && <p role="alert" className="text-sm text-rose-600">{error}</p>}
        {result && (
          <div className="text-sm text-slate-700 space-y-2">
            <p>输入 {result.InputCount} 笔；最终接单 {result.AcceptedCount} 笔；未分配 {result.UnassignedCount} 笔。</p>
            {result.pipeline && (
              <div className="grid gap-2 rounded-lg bg-blue-50 p-3 text-xs sm:grid-cols-2">
                <p>候选 / 邀请 worker：{result.pipeline.candidateWorkers} / {result.pipeline.offerWorkers}</p>
                <p>Top-K / Batch 大小：{result.pipeline.topK} / {result.pipeline.batchSize}</p>
                <p>Batch / 候选对象池容量：{result.pipeline.batchPoolCapacity} / {result.pipeline.candidatePoolCapacity}</p>
                <p>候选队列 / 乱序峰值：{result.pipeline.maxCandidateQueueDepth} / {result.pipeline.maxReorderDepth}</p>
                <p className="sm:col-span-2">各候选 worker 实际处理：{(result.pipeline.candidateWorkerCompleted ?? []).join(" / ") || "—"} 笔</p>
              </div>
            )}
            {assignments.length > 0 && <p className="font-medium text-emerald-700">最终分配：{assignments.map((item) => `订单 ${item.OrderID} → 骑手 ${item.RiderUID}`).join("；")}</p>}
            <h4 className="font-medium text-slate-800">逐次邀请结果</h4>
            <ol className="list-decimal space-y-1 pl-5">
              {attempts.length === 0 && <li>没有发出邀请。</li>}
              {attempts.map((attempt) => (
                <li key={attempt.RequestID}>订单 {attempt.OrderID} → 骑手 {attempt.RiderUID}：{attemptLabel(attempt.Status)}</li>
              ))}
            </ol>
          </div>
        )}
      </div>
    </section>
  );
}
