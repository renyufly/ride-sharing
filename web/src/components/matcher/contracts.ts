export type MatcherArrivalModel =
  | "uniform-window"
  | "front-loaded-burst"
  | "unbounded";

export type MatcherDistribution = "uniform" | "hotspot" | "skewed";

export type MatcherAlgorithm = "brute-force" | "kd-tree";

export type MatcherStrategy = "nearest" | "balanced";

// ==========================================
// 2. 请求契约 (Request DTO)
// ==========================================

export interface MatcherRunRequest {
  riderCount: number;
  orderCount: number;
  arrivalWindow: string;
  arrivalModel: MatcherArrivalModel;
  seed: number;
  riderDistribution: MatcherDistribution;
  orderDistribution: MatcherDistribution;
  algorithm: MatcherAlgorithm;
  strategy: MatcherStrategy;
  workers: number;
  batchSize: number;
  channelCapacity: number;
  topK: number;
  maxExtraDistanceMeters: number;
  maxOrdersPerRider: number;
  previewSize: number;
}

export interface MatcherRunConfig extends MatcherRunRequest {
  attempt: number;
}

// ==========================================
// 3. 错误响应 (Error Response)
// ==========================================

export interface MatcherErrorResponse {
  error: string;
}

// ==========================================
// 4. 地图展示类型 (Map Data & Geo Entities)
// ==========================================

export interface GeoPoint {
  latitude: number;
  longitude: number;
}

export interface MatcherBounds {
  minLatitude: number;
  maxLatitude: number;
  minLongitude: number;
  maxLongitude: number;
}

export interface RiderPreview {
  uid: number;
  location: GeoPoint;
}

export interface OrderPreview {
  /**
   * 注意：来自 Go uint64，存在溢出安全整数的可能。
   * 请勿用于地图关联，地图应使用 sequence。
   */
  id: number;
  sequence: number;
  plannedArrivalNs: number;
  pickup: GeoPoint;
}

export interface AssignmentPreview {
  /**
   * 注意：来自 Go uint64，存在溢出安全整数的可能。
   * 请勿用于地图关联，地图应使用 sequence。
   */
  orderId: number;
  sequence: number;
  riderUid: number;
  distanceMeters: number;
}

export interface MatcherMapData {
  bounds: MatcherBounds;
  riders: RiderPreview[];
  orders: OrderPreview[];
  assignments: AssignmentPreview[];
}

// ==========================================
// 5. 计数与守恒类型 (Counts)
// ==========================================

export interface MatcherCounts {
  generated: number;
  admitted: number;
  completed: number;
  unfinished: number;
  deferred: number;
  deferredByCapacity: number;
  deferredByWindow: number;
  generatedWithinWindow: number;
  admittedWithinWindow: number;
  completedWithinWindow: number;
  completionConserved: boolean;
  deferredConserved: boolean;
}

// ==========================================
// 6. 耗时与吞吐类型 (Timing)
// ==========================================

export interface MatcherTiming {
  riderGenerationNs: number;
  indexBuildNs: number;
  pipelineNs: number;
  totalNs: number;
  throughputPerSecond: number;
}

// ==========================================
// 7. 延迟统计与性能指标 (Latency & Performance)
// ==========================================

export interface LatencySummary {
  count: number;
  minNs: number;
  p50Ns: number;
  p95Ns: number;
  p99Ns: number;
  maxNs: number;
}

export interface AlgorithmPerformanceMetrics {
  nearestSearch: LatencySummary;
  candidateSearch: LatencySummary;
  assignmentDecision: LatencySummary;
  algorithmCompute: LatencySummary;
}

export interface MatcherPerformance {
  plannedArrivalWindowNs: number;
  actualInjectionNs: number;
  injectionOverrunNs: number;
  drainAfterWindowNs: number;
  totalRunNs: number;
  actualAdmissionRatePerSecond: number;
  actualCompletionRatePerSecond: number;
  admissionDelay: LatencySummary;
  queueAndMatchLatency: LatencySummary;
  endToEndLatency: LatencySummary;
  algorithmPerformanceMetrics: AlgorithmPerformanceMetrics;
}

// ==========================================
// 8. 均衡度与公平性 (Fairness)
// ==========================================

export interface BottomRider {
  riderUid: number;
  orderCount: number;
}

export interface MatcherFairness {
  assignmentCount: number;
  riderOrderCountSum: number;
  bottom10: BottomRider[];
  zeroRiderCount: number;
  minOrders: number;
  meanOrders: number;
  maxOrders: number;
  varianceOrders: number;
  stdDevOrders: number;
  coefficientOfVariation: number;
  averageDistanceMeters: number;
  p95DistanceMeters: number;
  maxDistanceMeters: number;
  distanceHistogramOverflow: number;
}

// ==========================================
// 9. 资源、索引与特定策略详情 (Resources & Internals)
// ==========================================

export interface MatcherResources {
  sampleInterval: string;
  elapsedNs: number;
  samples: number;
  startHeapAllocBytes: number;
  endHeapAllocBytes: number;
  peakHeapAllocBytes: number;
  peakRuntimeSysBytes: number;
  peakHeapInuseBytes: number;
  totalAllocDeltaBytes: number;
  mallocsDelta: number;
  freesDelta: number;
  numGCDelta: number;
  gcPauseDeltaNs: number;
  peakGoroutines: number;
  peakStackInuseBytes: number;
  peakStackSysBytes: number;
}

export interface MatcherIndex {
  kind: string;
  entryCount: number;
  entrySizeBytes: number;
  estimatedBytes: number;
}

export interface MatcherStrategyMetrics {
  topK: number;
  maxExtraDistanceMeters: number;
  reorderWindow: number;
  maxCandidateQueueDepth: number;
  maxReorderDepth: number;
  candidateWorkerCompletedOrders: number[];
}

// ==========================================
// 10. 延迟重试订单 (Deferred Details)
// ==========================================

export interface DeferredOrderItem {
  order: OrderPreview;
  reason: string;
  deferredAtNs: number;
  attempt: number;
}

export interface MatcherDeferredData {
  orders: DeferredOrderItem[];
  total: number;
}

// ==========================================
// 11. 聚合响应契约 (Overall Response DTO)
// ==========================================

export interface MatcherRunResponse {
  config: MatcherRunConfig;
  counts: MatcherCounts;
  timing: MatcherTiming;
  map: MatcherMapData;
  performance: MatcherPerformance;
  fairness: MatcherFairness;
  resources: MatcherResources;
  index: MatcherIndex;
  /**
   * 仅在策略 B (balanced 等进阶策略) 时返回，策略 A (nearest) 为 undefined
   */
  strategy?: MatcherStrategyMetrics;
  deferred: MatcherDeferredData;
  /**
   * 扩展
   */
  runId: string;
  currentRound: number;
  rounds: MatcherRoundResult[];
}

export interface MatcherRetryRequest {
  runId: string;
  sourceRound: number;
  maxOrdersPerRider: number;
  gracePeriodMs: number;
}

export interface MatcherRoundResult {
  round: number;
  inputSource: "generated" | "deferred";
  sourceRound: number;
  inputCount: number;
  matchedCount: number;
  deferredTotal: number;
  deferredCapacity: number;
  deferredWindow: number;
  maxOrdersPerRider: number;
  gracePeriodMs: number;
  deferredSamples: DeferredOrderItem[];
}
