"use client";

import "leaflet/dist/leaflet.css";
import { useEffect } from "react";
import { CircleMarker, MapContainer, Polyline, Popup, Rectangle, TileLayer, useMap, useMapEvents } from "react-leaflet";
import type { LatLngBoundsExpression } from "leaflet";

export type MapPoint = { latitude: number; longitude: number };
export type MapRider = { uid: number; location: MapPoint; connected: boolean; hasOffer: boolean };
export type MapOrder = { orderId: number; pickup: MapPoint; status?: string };

const CENTER: [number, number] = [37.76, -122.435];
const EARTH_RADIUS = 6371000;
const LAT_SCALE = EARTH_RADIUS * Math.PI / 180;
const LON_SCALE = LAT_SCALE * Math.cos(CENTER[0] * Math.PI / 180);

function metersBetween(first: MapPoint, second: MapPoint) {
  return Math.hypot(
    (first.longitude - second.longitude) * LON_SCALE,
    (first.latitude - second.latitude) * LAT_SCALE,
  );
}

function ClickHandler({ onPlace }: { onPlace: (point: MapPoint) => void }) {
  useMapEvents({ click: (event) => onPlace({ latitude: event.latlng.lat, longitude: event.latlng.lng }) });
  return null;
}

function MapResizeUpdater() {
  const map = useMap();
  useEffect(() => {
    const observer = new ResizeObserver(() => map.invalidateSize());
    observer.observe(map.getContainer());
    return () => observer.disconnect();
  }, [map]);
  return null;
}

export function LiveDispatchMap({ riders, orders, attempts, selectedOrderId, topK, extraMeters, placementEnabled, onPlace, onSelectOrder }: {
  riders: MapRider[];
  orders: MapOrder[];
  attempts: Array<{ OrderID: number; RiderUID: number; Status: string }>;
  selectedOrderId: number | null;
  topK: number;
  extraMeters: number;
  placementEnabled: boolean;
  onPlace: (point: MapPoint) => void;
  onSelectOrder: (id: number) => void;
}) {
  const selected = orders.find((order) => order.orderId === selectedOrderId);
  const ranked = selected
    ? riders.filter((rider) => rider.connected)
      .map((rider) => ({ ...rider, distance: metersBetween(selected.pickup, rider.location) }))
      .sort((a, b) => a.distance - b.distance || a.uid - b.uid)
      .slice(0, topK)
    : [];
  const nearest = ranked[0]?.distance ?? 0;
  const radius = nearest + extraMeters;
  const bounds: LatLngBoundsExpression | null = selected && ranked.length > 0 ? [
    [selected.pickup.latitude - radius / LAT_SCALE, selected.pickup.longitude - radius / LON_SCALE],
    [selected.pickup.latitude + radius / LAT_SCALE, selected.pickup.longitude + radius / LON_SCALE],
  ] : null;
  const rankedUIDs = new Set(ranked.map((rider) => rider.uid));

  return (
    <div className="space-y-2">
      <div className="h-[420px] overflow-hidden rounded-xl border border-slate-200 md:h-[560px]">
        <MapContainer center={CENTER} zoom={12} scrollWheelZoom={false} className="z-0 h-full w-full">
          <MapResizeUpdater />
          <TileLayer attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors' url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png" />
          {placementEnabled && <ClickHandler onPlace={onPlace} />}
          {bounds && <Rectangle bounds={bounds} pathOptions={{ color: "#2563eb", weight: 2, dashArray: "7 5", fillOpacity: 0.06 }} />}
          {selected && ranked.map((rider) => (
            <Polyline key={`candidate-${rider.uid}`} positions={[[selected.pickup.latitude, selected.pickup.longitude], [rider.location.latitude, rider.location.longitude]]} pathOptions={{ color: "#60a5fa", weight: 1, opacity: 0.6 }} />
          ))}
          {selected && attempts.filter((attempt) => attempt.OrderID === selected.orderId).map((attempt, index) => {
            const rider = riders.find((item) => item.uid === attempt.RiderUID);
            if (!rider) return null;
            return <Polyline key={`attempt-${index}`} positions={[[selected.pickup.latitude, selected.pickup.longitude], [rider.location.latitude, rider.location.longitude]]} pathOptions={{ color: attempt.Status === "accepted" ? "#059669" : "#e11d48", weight: 4, opacity: 0.9 }} />;
          })}
          {orders.map((order) => (
            <CircleMarker key={`order-${order.orderId}`} center={[order.pickup.latitude, order.pickup.longitude]} radius={order.orderId === selectedOrderId ? 9 : 6} bubblingMouseEvents={false} eventHandlers={{ click: () => onSelectOrder(order.orderId) }} pathOptions={{ color: "#c2410c", fillColor: "#fb923c", fillOpacity: 0.95, weight: 2 }}>
              <Popup>订单 {order.orderId}<br />{order.status ?? "搜索骑手中"}<br />点击查看附近骑手</Popup>
            </CircleMarker>
          ))}
          {riders.map((rider) => (
            <CircleMarker key={`rider-${rider.uid}`} center={[rider.location.latitude, rider.location.longitude]} radius={rider.hasOffer ? 10 : 7} bubblingMouseEvents={false} pathOptions={{ color: rider.hasOffer ? "#b45309" : rider.connected ? "#047857" : "#64748b", fillColor: rider.hasOffer ? "#fbbf24" : rider.connected ? rankedUIDs.has(rider.uid) ? "#34d399" : "#10b981" : "#94a3b8", fillOpacity: 0.95, weight: 2 }}>
              <Popup>骑手 {rider.uid}<br />{rider.hasOffer ? "等待接单或拒单" : rider.connected ? "WebSocket 在线" : "未连接"}{selected && rankedUIDs.has(rider.uid) && <><br />距订单约 {Math.round(metersBetween(selected.pickup, rider.location))} 米</>}</Popup>
            </CircleMarker>
          ))}
        </MapContainer>
      </div>
      <p className="text-xs text-slate-500">
        绿点：在线骑手；灰点：离线；黄点：收到邀请；橙点：订单。蓝线表示按当前上报位置计算的最近 {Math.min(topK, riders.filter((rider) => rider.connected).length)} 位在线骑手。
        {selected && ranked.length > 0 && ` 虚线框是“最近距离 ${Math.round(nearest)} 米 + 额外 ${extraMeters} 米”的可视参考，约 ${Math.round(radius)} 米半径。`}
        绿色粗线表示最终接单，红色粗线表示拒单、超时或发送失败。实际候选由服务端 Top-K worker 搜索，框不是 KD-tree 的访问轨迹；实际派单以 WebSocket 邀请和运行结果为准。
      </p>
    </div>
  );
}
