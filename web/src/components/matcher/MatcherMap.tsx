"use client";

import { useEffect, useMemo } from "react";
import "leaflet/dist/leaflet.css";
import {
  MapContainer,
  TileLayer,
  CircleMarker,
  Polyline,
  Popup,
  useMap,
} from "react-leaflet";
import type { LatLngBoundsExpression, LatLngExpression } from "leaflet";
import type {
  MatcherMapData,
  MatcherBounds,
  OrderPreview,
  RiderPreview,
} from "./contracts";

interface MatcherMapProps {
  data: MatcherMapData;
}

interface AssignmentLine {
  key: string;
  sequence: number;
  riderUid: number;
  distanceMeters: number;
  positions: [LatLngExpression, LatLngExpression];
}

// 内部组件：负责监听 bounds 并在数据刷新时平滑缩放/平移视口
function MapBoundsUpdater({ bounds }: { bounds: MatcherBounds }) {
  const map = useMap();

  useEffect(() => {
    if (!bounds) return;

    const leafletBounds: LatLngBoundsExpression = [
      [bounds.minLatitude, bounds.minLongitude],
      [bounds.maxLatitude, bounds.maxLongitude],
    ];

    map.fitBounds(leafletBounds, {
      padding: [24, 24],
      maxZoom: 16,
      animate: true,
    });
  }, [map, bounds]);

  return null;
}

export function MatcherMap({ data }: MatcherMapProps) {
  const { bounds, riders, orders, assignments } = data;

  // 计算初始中心点（兜底使用 0, 0）
  const center: LatLngExpression = useMemo(() => {
    if (!bounds) return [0, 0];
    const centerLat = (bounds.minLatitude + bounds.maxLatitude) / 2;
    const centerLng = (bounds.minLongitude + bounds.maxLongitude) / 2;
    return [centerLat, centerLng];
  }, [bounds]);

  // 1. 建立基于 sequence 和 riderUid 的查找表
  const ordersBySequence = useMemo(() => {
    const map = new Map<number, OrderPreview>();
    for (const order of orders) {
      map.set(order.sequence, order);
    }
    return map;
  }, [orders]);

  const ridersByUID = useMemo(() => {
    const map = new Map<number, RiderPreview>();
    for (const rider of riders) {
      map.set(rider.uid, rider);
    }
    return map;
  }, [riders]);

  // 2. 匹配 assignment 连线（丢弃两端不匹配的脏数据，避开 Go uint64 溢出隐患）
  const assignmentLines = useMemo(() => {
    const lines: AssignmentLine[] = [];

    for (const assignment of assignments) {
      const order = ordersBySequence.get(assignment.sequence);
      const rider = ridersByUID.get(assignment.riderUid);

      // 任一端不存在时跳过，避免局部抽样数据引发崩溃
      if (!order || !rider) continue;

      lines.push({
        key: `line-${assignment.sequence}-${assignment.riderUid}`,
        sequence: assignment.sequence,
        riderUid: assignment.riderUid,
        distanceMeters: assignment.distanceMeters,
        positions: [
          [order.pickup.latitude, order.pickup.longitude],
          [rider.location.latitude, rider.location.longitude],
        ],
      });
    }

    return lines;
  }, [assignments, ordersBySequence, ridersByUID]);

  return (
    <div className="flex flex-col gap-2 w-full">
      {/* 顶部/状态栏说明 */}
      <div className="flex flex-wrap items-center justify-between text-xs text-slate-500 px-1">
        <div className="flex items-center gap-4">
          <span className="flex items-center gap-1.5">
            <span className="w-2.5 h-2.5 rounded-full bg-blue-500 inline-block" />
            骑手抽样:{" "}
            <strong className="text-slate-700">{riders.length}</strong>
          </span>
          <span className="flex items-center gap-1.5">
            <span className="w-2.5 h-2.5 rounded-full bg-amber-500 inline-block" />
            订单抽样:{" "}
            <strong className="text-slate-700">{orders.length}</strong>
          </span>
          <span className="flex items-center gap-1.5">
            <span className="w-2.5 h-1 bg-emerald-500/60 inline-block rounded-xs" />
            匹配连线:{" "}
            <strong className="text-slate-700">{assignmentLines.length}</strong>
          </span>
        </div>
        <span className="text-slate-400">注：地图仅展示前端抽样数据预览</span>
      </div>

      {/* 地图视口容器：必须显式指定高度（移动端 420px，桌面端 560px） */}
      <div className="w-full h-[420px] md:h-[560px] rounded-lg border border-slate-200 overflow-hidden relative shadow-xs">
        <MapContainer
          center={center}
          zoom={13}
          scrollWheelZoom={false}
          className="w-full h-full z-0"
        >
          {/* 1. 底图图层 */}
          <TileLayer
            attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
            url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
          />

          {/* 视口自适应控制器 */}
          <MapBoundsUpdater bounds={bounds} />

          {/* 2. 匹配连线图层 (最底层线段，避免盖住点) */}
          {assignmentLines.map((line) => (
            <Polyline
              key={line.key}
              positions={line.positions}
              pathOptions={{
                color: "#10b981", // 祖母绿 (Emerald)
                weight: 2,
                opacity: 0.5,
              }}
            >
              <Popup>
                <div className="text-xs space-y-1">
                  <div>
                    <strong>订单 Sequence:</strong> #{line.sequence}
                  </div>
                  <div>
                    <strong>分配骑手 UID:</strong> {line.riderUid}
                  </div>
                  <div>
                    <strong>匹配距离:</strong> {line.distanceMeters.toFixed(1)}{" "}
                    米
                  </div>
                </div>
              </Popup>
            </Polyline>
          ))}

          {/* 3. 订单图层 (橙色点) */}
          {orders.map((order) => (
            <CircleMarker
              key={`order-${order.sequence}`}
              center={[order.pickup.latitude, order.pickup.longitude]}
              radius={5}
              pathOptions={{
                color: "#ea580c",
                fillColor: "#f97316",
                fillOpacity: 0.85,
                weight: 1.5,
              }}
            >
              <Popup>
                <div className="text-xs space-y-1">
                  <div>
                    <strong>订单 Sequence:</strong> #{order.sequence}
                  </div>
                  <div>
                    <strong>计划到达:</strong>{" "}
                    {(order.plannedArrivalNs / 1e6).toFixed(2)} ms
                  </div>
                  <div>
                    <strong>坐标:</strong> {order.pickup.latitude.toFixed(5)},{" "}
                    {order.pickup.longitude.toFixed(5)}
                  </div>
                </div>
              </Popup>
            </CircleMarker>
          ))}

          {/* 4. 骑手图层 (蓝色点，最顶层) */}
          {riders.map((rider) => (
            <CircleMarker
              key={`rider-${rider.uid}`}
              center={[rider.location.latitude, rider.location.longitude]}
              radius={6}
              pathOptions={{
                color: "#1d4ed8",
                fillColor: "#3b82f6",
                fillOpacity: 0.9,
                weight: 1.5,
              }}
            >
              <Popup>
                <div className="text-xs space-y-1">
                  <div>
                    <strong>骑手 UID:</strong> {rider.uid}
                  </div>
                  <div>
                    <strong>位置:</strong> {rider.location.latitude.toFixed(5)},{" "}
                    {rider.location.longitude.toFixed(5)}
                  </div>
                </div>
              </Popup>
            </CircleMarker>
          ))}
        </MapContainer>
      </div>
    </div>
  );
}
