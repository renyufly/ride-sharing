# Ride Sharing Matcher

该项目包含一个可配置、可观测的骑手与订单匹配系统，支持订单流量模型、最近骑手策略、Top-K 负载均衡策略、骑手容量限制、Deferred 订单重试及前端结果展示。

## 启动项目

启动前请确保 Docker、Kubernetes、kubectl 和 Tilt 已安装并可用。在项目根目录执行：

```bash
tilt up
```

等待 Tilt 中相关资源启动完成后，访问：

- 前端首页：<http://localhost:3000>
- 匹配系统：<http://localhost:3000/matcher>
- Matcher API：<http://localhost:8082>
- Tilt 控制台：<http://localhost:10350>

`/matcher` 页面可以配置骑手数、订单数、到达窗口、流量模型、匹配算法、调度策略、Top-K、骑手容量及并发参数，并查看吞吐、延迟、公平性、资源使用、地图和 Deferred 重试结果。

## 匹配系统设计

匹配系统的主要调用链如下：

```text
Matcher 前端
    ↓ POST /matcher/run 或 /matcher/retry
Matcher API
    ↓
Match Runner
    ├─ 生成骑手与订单
    ├─ 构建空间索引
    ├─ 启动资源监控
    └─ 执行 Pipeline
          ├─ Producer
          ├─ 有界 Batch Channel
          ├─ Candidate Worker Pool
          └─ Coordinator
                 ├─ Assignment
                 └─ Deferred Sink
```

系统提供两种调度策略：

- `nearest`：将订单分配给距离最近的骑手。
- `balanced`：先搜索最近的 Top-K 骑手，再在距离限制和容量限制内优先选择当前负载较低的骑手。

Balanced Pipeline 允许 Candidate Worker 并行搜索候选，但由单个 Coordinator 按订单 Sequence 顺序完成最终决策。Coordinator 独占骑手负载状态，从而避免数据竞争和并发超额分配，并保持结果可重复。

无法完成分配的订单进入 Deferred：

- `capacity_exhausted`：符合条件的候选骑手均达到容量上限。
- `window_expired`：订单轮到最终决策时已经超过分配窗口。

CLI 使用 JSONL 保存和重放 Deferred 订单；Web 页面使用内存 Session 发起下一轮 Retry。

## `internal` 目录组织

```text
internal/
├─ config/       运行参数、默认值、命令行解析与配置校验
├─ deferred/     Deferred 订单的文件/内存存储与重放
├─ generator/    骑手和订单生成，以及订单到达模型
├─ geo/          经纬度边界、坐标投影及距离计算基础
├─ matcher/      候选骑手搜索接口及空间搜索算法
│  ├─ bruteforce/  全量扫描搜索
│  └─ kdtree/      KD-Tree 最近邻与 Top-K 搜索
├─ matcherapi/   Matcher HTTP 请求、响应、Handler 与 DTO 映射
├─ matchrun/     CLI 和 HTTP 共用的一次匹配实验编排器
├─ model/        Rider、Order、Assignment 等核心领域模型
├─ pipeline/     Producer、Worker、Coordinator、Balanced 和性能统计
├─ report/       骑手负载公平性及匹配距离报告
└─ resource/     Go 堆内存、GC、Goroutine 等运行资源监控
```

### 各层职责

- `config`：统一表达订单量、时间窗口、到达模型、算法、策略、并发参数、容量和 Deferred 配置。
- `generator`：根据固定 Seed 生成可重复数据，支持 `uniform-window`、`front-loaded-burst` 和 `unbounded` 到达模型。
- `geo`：将地理坐标转换为适合空间搜索的二维坐标。
- `matcher`：只负责候选搜索，不维护骑手负载；可选择 Brute Force 或 KD-Tree。
- `pipeline`：负责有界并发、背压、候选搜索、顺序协调、最终分配、Deferred 判定和延迟统计。
- `deferred`：通过 Source/Sink 抽象提供 JSONL 持久化及 Web 内存重试。
- `matchrun`：组装 Generator、Matcher、Pipeline、Deferred 和 Resource Monitor，供 CLI 与 API 共用。
- `matcherapi`：负责 HTTP 边界校验、单任务运行控制、Retry Session 和前端响应转换。
- `report`：计算每名骑手订单数、零订单骑手、标准差、变异系数及匹配距离分布。
- `resource`：采样内存、分配、GC、Goroutine 和栈使用情况。

这种组织将候选搜索、调度决策、运行编排、HTTP 协议和展示层解耦，便于独立测试、替换算法以及扩展新的订单来源或 Deferred 存储方式。

│ │ └─ MatcherMap.tsx Matcher 骑手、订单及分配关系地图
│ ├─ ui/ Card、Button、Avatar 等通用 UI 基础组件
│ └─ \*.tsx 首页使用的司机、骑手、路线和支付组件
├─ hooks/ 司机与骑手实时连接 Hook
├─ assets/ 前端静态资源
├─ lib/ 通用样式与工具函数
├─ utils/ Geohash 和数学工具
├─ contracts.ts 拼车业务的 HTTP/WebSocket 协议
├─ types.ts Trip、Driver、Route 等共享业务类型
└─ constants.ts 前端共享常量

```

Matcher 页面按入口、状态行为、展示和纯辅助逻辑拆分：`page.tsx` 不包含具体业务流程；`useMatcherPage.ts` 负责运行实验和 Deferred Retry 的状态及请求；`MatcherView.tsx` 只消费页面模型并渲染配置、统计结果和地图；`matcher_helpers.ts` 保存无 React 状态依赖的默认值、校验和格式化逻辑。HTTP 数据结构集中定义在 `components/matcher/contracts.ts`，地图作为独立组件按客户端能力动态加载，避免 Leaflet 参与服务端渲染。
```
