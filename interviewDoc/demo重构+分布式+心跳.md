# demo重构+分布式+心跳-2026.10.25

原始参考：

```
├─ cmd/service/          # 主入口，组装依赖
├─ internal/
│   ├─ app/              # 应用服务（用例编排）
│   ├─ domain/           # 领域模型、仓储接口
│   ├─ adapter/          # DB/Cache/MQ/HTTP 实现
│   ├─ platform/         # 配置、日志、监控、DI
│   └─ transport/http/   # 路由、中间件、DTO
├─ pkg/                  # 可复用库
├─ api/                  # OpenAPI/Protobuf 契约
└─ deployments/          # Helm/Manifest
```

## 现有改造计划：

```
cmd/
  matcher-api/                 前端请求、运行查询的服务入口
  scheduler/                   分布式匹配与派单 Worker 入口
  realtime/                    骑手和用户长连接服务入口

internal/
  domain/
    geo/                       坐标、投影、距离
    order/                     订单身份与状态规则
    rider/                     骑手身份、可用性、容量规则
    matching/                  候选、Top-K 及负载选择规则
    dispatch/                  派单尝试、接单、拒单、超时规则

  app/
    run/                       一次运行的参数、生命周期、进度
      replay/                  现有 Deferred 订单的轮次重放
    matching/                  查询候选并执行匹配策略
    dispatch/                  发起请求、处理应答和重新派单
    presence/                  心跳、在线状态与连接有效期
    report/                    Bottom 10、距离、延迟及派单结果统计

  adapter/
    simulation/
      orders/                  按参数生成订单
      riders/                  按参数生成骑手
      rider_client/            模拟骑手连接、心跳和应答
    spatial/
      kdtree/                  Top-K 空间索引实现
    riderstate/
      memory/                  单实例运行时的骑手负载状态
    deferred/
      file/                    JSONL Deferred 读写
      memory/                  内存 Deferred 读写
    session/
      memory/                  现有 Run/Retry 会话存储
    persistence/               订单、派单尝试和运行状态持久化
    cache/                     在线状态、位置及连接路由缓存
    messaging/                 跨实例事件发布
    realtime/                  本实例连接管理与定向投递

  platform/
    config/                    各进程配置与校验
    logging/                   结构化日志
    metrics/                   运行和系统指标
    tracing/                   跨服务链路追踪

  transport/
    http/
      run/                     创建运行、读取进度和结果的 DTO/Handler
    ws/
      rider/                   骑手认证、心跳、接单与拒单消息
      user/                    用户连接与结果通知
    message/
      order/                   订单进入调度系统的消息入口
      response/                跨实例应答和状态事件入口

api/
  http/                       前端与服务端的稳定请求/响应契约
  realtime/                   WebSocket 消息契约
  events/                     跨进程事件契约

web/                            保留现有 matcher 页面并随契约演进
infra/                          沿用现有 Docker、Kubernetes 目录
```

匹配算法不会认识 WebSocket 或消息队列；模拟负载不会成为真实订单处理的必经步骤；拒单重派也不会复用“重跑一批 Deferred 订单”的实验语义

- 舍弃 CLI，策略A

`simulation` 只提供订单、骑手和模拟应答；订单进入后使用同一个 `app/matching → app/dispatch` 流程。后续分布式化不会另造一套匹配规则。

`report` 保留原有“算法选中次数”的 Bottom 10，同时新增“最终接单次数”的统计。拒单和重派不会悄悄改变旧指标的含义。

三个 `cmd` 是**同一业务流程的不同运行角色**：API 接收前端运行请求，Scheduler 处理分片订单，Realtime 维护连接并转送应答。

流程图：

```
前端 /matcher：设置骑手数、订单数、到达模型、匹配参数
                         │
                         ▼
              matcher-api：创建运行 RunID
                         │
             ┌───────────┴───────────┐
             ▼                       ▼
        生成订单流                生成骑手
             │                       │
             │                 模拟骑手客户端
             │                       │ 认证 userID、建立长连接、
             │                       │ 持续发送心跳
             │                       ▼
             │                  realtime 服务
             │                       │
             │                       ▼
             │                在线状态／连接路由
             │                       │
             ▼                       │
      有界队列与区域分片                │
             │                       │
             ▼                       │
    Scheduler：每分片调度者 ◄───────——─┘
             │                 查询在线状态
             ▼
       KD-tree 查询 Top-K
             │
             ▼
   距离＋负载＋容量规则选择候选骑手
             │
             ▼
      确认容量并记录派单尝试
             │
             ▼
      消息投递 → realtime → 骑手长连接
                              │
                    ┌─────────┼──────────┐
                    ▼         ▼          ▼
                   接单      拒单     超时／离线
                    │         │          │
                    ▼         └────┬─────┘
          校验当前派单尝试有效      │ 结束尝试、释放占用
                    │              ▼
                    ▼         选择下一候选
             提交唯一最终分配       │
                    │         无可用候选时进入
                    ▼         Deferred／后续重试
              通知用户连接
                    │
                    ▼
       运行报告与前端展示
       ├─ 原有 Bottom 10：算法选中次数
       ├─ 新增 Bottom 10：最终接单次数
       └─ 生成／匹配／拒绝／超时／Deferred、
          距离、延迟及资源指标
```

后续增加分片、消息和多实例，是扩展图中的**队列与 Scheduler 部分**。

前端启动一次运行，生成的订单和骑手进入同一条派单流程；分布式调度改变流程的执行方式，心跳为流程持续提供骑手在线状态。

| 阶段            | 发生什么                                                                                                           | 主要目录                                                                                          |
| --------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------- |
| 1. 创建运行     | 前端提交订单量、骑手量、到达模型、Top-K、容量等参数；系统创建 RunID、校验参数并控制运行资源                        | `transport/http/run`、`app/run`、`platform/config`                                                |
| 2. 产生输入     | 按 seed 生成骑手和订单；订单按到达时间流式进入有界队列。完整网络场景中的模拟骑手也建立连接并发送心跳               | `adapter/simulation`、`app/run`                                                                   |
| 3. 调度与匹配   | 单实例时由一个调度者处理；扩容后按地理区域送到相应分片。查询 KD-tree Top-K，结合距离、负载、容量和在线状态选择候选 | `app/dispatch`、`app/matching`、`domain/matching`、`adapter/spatial/kdtree`                       |
| 4. 请求骑手应答 | 为选中的骑手记录有期限的派单尝试，再将请求送到持有该骑手连接的节点                                                 | `domain/dispatch`、`app/dispatch`、`adapter/persistence`、`adapter/messaging`、`adapter/realtime` |
| 5. 确认或重派   | 骑手接单才提交最终分配；拒单、超时或离线则结束该尝试、释放占用并选择下一候选。无候选时进入待处理状态               | `transport/ws/rider`、`app/dispatch`、`domain/dispatch`                                           |
| 6. 汇总展示     | 持续记录运行进度，结束后展示地图、Bottom 10、公平性、距离、延迟、Deferred 和最终派单结果                           | `app/report`、`transport/http/run`、`web`                                                         |

- `transport/ws/rider` 验证连接身份，`app/presence` 管理在线有效期
- 原有 Deferred 订单的**整轮重放**放在 `app/run/replay`；骑手拒单后的**单笔订单重派**放在 `app/dispatch`

## 原有demo迁移步骤

### 本轮范围与约束

本轮只迁移**现有 `internal/` 能力**，不实现上文规划的分布式调度、心跳、长连接、派单应答或持久化。上文是后续目标架构，不表示本轮要建齐所有目录。

- 保留 `cmd/matcher` CLI 和 `cmd/matcherapi` HTTP 服务两个现有入口；上文“舍弃 CLI”不适用于本轮。
- 保持 `POST /matcher/run`、`POST /matcher/retry` 的路径、请求与响应字段、状态码以及同步返回方式。`web/` 本轮无需随内部包迁移而修改。
- 保持 nearest/balanced 的选人规则、容量规则、Deferred 整轮重放、报告统计及现有 `Assignment`/`CompletedOrders` 含义；本轮不引入“骑手接单后才算最终分配”的新语义。
- 只建立承接现有代码的目录。`dispatch`、`presence`、`ws`、`messaging`、`persistence`、`cache`、`tracing` 等后续目录暂不创建。
- 每一阶段先确定目标包的责任和依赖，再迁移现有代码及其引用；避免仅改目录名，也避免在新旧包中长期复制同一类型。

### 步骤 1：列出当前调用链和包边界

从两个入口向内梳理：`cmd/matcherapi → matcherapi → matchrun → config/generator/matcher/pipeline/deferred/report/resource`，以及 `cmd/matcher → matchrun`。记录每个包提供的类型、接口、构造和调用方，尤其是 `matchrun.Run`、HTTP DTO、CLI 输出映射。后续按依赖从底层向入口迁移，避免一次性移动整个 `internal/`。

**步骤一盘点结果（2026-10-25；仅记录现状，尚未迁移代码）：**

| 当前层 | 现有入口、类型和责任 | 直接依赖及迁移关注点 |
| --- | --- | --- |
| `cmd/matcherapi/main.go` | 注册 `POST /matcher/run`、`POST /matcher/retry`，配置 HTTP Server 和关闭流程 | 直接创建 `matcherapi.Handler`；路由与服务行为是本轮保留的入口契约 |
| `internal/matcherapi/request.go`、`response.go`、`response_mapper.go` | HTTP 请求校验、运行参数映射、响应 DTO 与结果映射 | 直接引用 `config`、`generator`、`matchrun`、`pipeline`、`deferred`、`model`、`geo`、`report`、`resource`、`matcher`；JSON 字段和映射需随包迁移保持原义 |
| `internal/matcherapi/handler.go` | 同步执行 Run/Retry；用 `runSlot` 限制并发；用 `lastSession` 保存最后一次运行的 RunID、轮次、Deferred 订单和轮次摘要 | 直接调用 `matchrun.Run`，Run 使用内存 Deferred sink；Retry 使用内存 Deferred source/sink，并校验 RunID、来源轮次及是否还有 Deferred 订单；会话只在本进程内有效 |
| `cmd/matcher/main.go` | 解析 CLI 参数，创建可选文件 Deferred sink，调用 `matchrun.Run`，将结果映射成 CLI JSON | 除 `matchrun` 外，还直接引用 `config`、`deferred`、`matcher`、`model`、`pipeline`、`report`、`resource`；迁移时须更新这些直接引用并保留 CLI 输出 |
| `internal/matchrun/request.go`、`runner.go`、`result.go` | 定义 `Request`/`Result`，校验配置，生成骑手，选择搜索实现与订单源，执行 nearest/balanced，汇总资源、时长和预览 | `Request` 注入可选 `pipeline.OrderSource` 和 `DeferredOrderSink`；`runner.go` 同时依赖 `config`、`generator`、`geo`、两种 matcher、`deferred`、`pipeline`、`resource`，是当前最集中的装配点 |
| `internal/pipeline` | `Run`、`RunBalanced`、`Options`、`BalancedOptions`、`Result`、性能与内存估算；定义 `OrderSource`、`Matcher`、`CandidateMatcher`、`DeferredOrderSink` | 直接依赖 `model`、`matcher` 规则和 `report`；nearest 并行处理订单，balanced 并行取候选后按顺序协调选择；`DeferredOrder` 与 sink 接口目前也定义在此包 |
| `internal/generator`、`geo`、`model` | seed 生成、骑手生成、订单流、坐标校验与投影、共享数据类型 | `generator → geo + model`，`geo → model`；`model` 当前没有匹配行为，是多数包共同引用的基础类型 |
| `internal/matcher`、`matcher/bruteforce`、`matcher/kdtree` | 候选及距离比较规则、两种搜索实现、索引统计 | 两种实现都依赖 `matcher` 规则和 `model`；`matchrun` 在运行时选择实现 |
| `internal/deferred`、`report`、`resource`、`config` | 文件/内存 Deferred 源与 sink，报告累加，运行时采样，默认值和 CLI 参数解析 | `deferred → pipeline + model`，`report → model`，`config → generator`；特别是 Deferred 对 `pipeline.DeferredOrder` 的依赖应随接口和数据归属一起处理 |

**当前数据流：**

1. HTTP Run：请求 JSON → `Request.toRunRequest` → Handler 获取运行槽并创建内存 Deferred sink → `matchrun.Run` → `pipeline.Run` 或 `RunBalanced` → `toRunResponse` → 同步返回；成功后 Handler 保存最后一场会话。
2. HTTP Retry：Retry JSON → 校验 RunID、当前轮次与 Deferred 集合 → 内存 Deferred source/sink → 再次调用 `matchrun.Run` → 更新轮次与最后一场会话 → 同步返回。这里是**整轮重放**，不是单笔订单重派。
3. CLI：`config.Parse` → 可选文件 Deferred sink → `matchrun.Run` → CLI 专用 DTO 映射 → stdout JSON；文件 sink 由 CLI 入口关闭。
4. `matchrun.Run`：校验请求 → 生成骑手与构建搜索器 → 使用生成订单流、文件 Deferred 输入或调用方提供的订单源 → 执行所选流水线 → 汇总预览、报告、性能和资源数据。

**本轮必须守住的边界：**HTTP 的请求/响应 DTO 和 CLI 的输出 DTO 属于各自入口；运行用例的 `Request`/`Result` 是内部契约；`model.Order`、`Rider`、`Assignment` 与 `pipeline.DeferredOrder` 是目前跨包共享的数据。后续迁移这些类型时应确定单一归属，同时更新直接调用方，不把 HTTP DTO 或 CLI DTO 当作领域模型。

**步骤一复核补充（对照当前 Go 代码）：**

| 包 | 对外类型、接口与构造/运行函数 | 当前直接调用方 |
| --- | --- | --- |
| `config` | `Config`、`DisplayConfig`、`Algorithm`、`Strategy`；`Default`、`Parse`、`Config.Validate`、`Config.Display` | `cmd/matcher` 解析和显示配置；`matcherapi` 映射 HTTP 请求；`matchrun` 校验并执行；`config` 自身引用 `generator` 的到达和空间分布类型 |
| `model`、`geo` | `GeoPoint`、`Point2D`、`Order`、`Rider`、`Assignment`；`BoundingBox`、`Projector`、`NewProjector`、`ValidatePoint` | `generator`、`matcher` 及其两种实现、`pipeline`、`deferred`、`report`、`matchrun`；HTTP/CLI 映射也读取这些值 |
| `generator` | `ArrivalModel`、`SpatialDistribution`、`Seeds`、`Generator`、`OrderStream`；`New`、`GenerateRiders`、`NewOrderStream`、`OrderStream.Next` | `config` 使用模式类型与校验；`matchrun` 创建骑手和订单流；`matcherapi` 将请求字符串转为模式类型 |
| `matcher`、`matcher/bruteforce`、`matcher/kdtree` | `Candidate`、`IndexStats`、候选比较/保留规则；两种实现各有 `New`、`Match`、`TopK`、`TopKInto`、`IndexStats` | `matchrun.createMatcher` 选取并构造实现；`pipeline` 使用 `Match` 或 `TopKInto`；HTTP/CLI 输出使用 `IndexStats` |
| `pipeline` | `OrderSource`、`Matcher`、`CandidateMatcher`、`DeferredOrderSink`；`Options`、`BalancedOptions`、`Result`、`DeferredOrder`、性能/内存统计类型；`Run`、`RunBalanced`、内存估算函数 | `matchrun` 装配并调用；`generator.OrderStream` 和 `deferred` 的 source/sink 满足接口；`matcherapi`、CLI 从结果映射输出 |
| `deferred` | `NewFileSink`/`FileSink.Store`/`Close`、`OpenFileSource`/`FileSource.Next`/`Close`、`NewMemorySink`/`MemorySink.Store`/`Snapshot`、`NewMemorySource`/`MemorySource.Next` | CLI 创建并关闭文件 sink；`matchrun` 打开并关闭文件 source；`matcherapi.Handler` 为 Run/Retry 创建内存 source/sink |
| `report`、`resource` | `Accumulator`/`New`/`Fork`/`Observe`/`Merge`/`Summary`、`report.Summary`；`Monitor`/`Start`/`Stop`、`resource.Stats` | `pipeline` 累加报告，`matchrun` 采样运行资源；`matcherapi`、CLI 展示汇总结果 |
| `matchrun` | `Request`（`Config`、预览数、可选 `OrderSource`/`DeferredSink`）、`Result`、`Timing`；`Run` | `matcherapi.Handler.Run`/`Retry` 与 `cmd/matcher.main` 是两个调用入口；`runner.go` 内部选择搜索实现、订单源和流水线 |
| `matcherapi` | `Handler`/`NewHandler`/`Run`/`Retry`、`Request`/`RetryRequest`、`RunResponse`/`RoundResponse` 及嵌套 DTO、`ErrorResponse` | `cmd/matcherapi` 注册两个 POST 路由；包内 `Request.toRunRequest` 负责输入映射，`toRunResponse`/`mapRoundResponse` 等负责输出映射和预览截断 |

**入口输出边界：**HTTP Handler 同步返回 `RunResponse`，其中 `runId`、`currentRound`、`rounds` 由单实例 `lastSession` 管理；请求格式或参数错误返回 400，运行占用及轮次冲突返回 409，缺失会话/RunID 返回 404，运行或存储失败返回 500，成功返回 200。CLI 在 `cmd/matcher/main.go` 内定义 `startupOutput`、`dataOutput` 和各预览 DTO，直接把 `matchrun.Result` 转为 stdout JSON；它不复用 HTTP DTO，且自行关闭文件 Deferred sink。两者都把当前的 `Assignment`/`CompletedOrders` 表达为算法完成分配，不代表骑手确认接单。

### 步骤 2：迁移领域值和匹配规则

| 现有代码 | 本轮目标 | 责任 |
| --- | --- | --- |
| `internal/geo`、`internal/model` 的 `GeoPoint`/`Point2D` | `internal/domain/geo` | 坐标校验、边界、投影和距离所需的坐标值 |
| `internal/model` 的 `Order`、`Rider` | `internal/domain/order`、`internal/domain/rider` | 现有订单与骑手数据；保留 ID、Sequence、计划到达时间和投影点的含义 |
| `internal/model` 的 `Assignment`、`internal/matcher` 的候选与比较规则 | `internal/domain/matching` | 匹配结果、距离比较、Top-K 候选排序及同距时较小 UID 优先的规则 |
| `internal/matcher/bruteforce`、`internal/matcher/kdtree` | `internal/adapter/spatial/bruteforce`、`internal/adapter/spatial/kdtree` | 两种具体搜索实现，共用同一套领域比较规则 |

先确定 `Assignment` 的唯一归属，再调整报告、流水线和响应映射的引用。保持 KD-tree 建索引与查询的现有行为，不在本轮加入动态位置更新。

**步骤二盘点结果（2026-10-25；以下归属与迁移顺序已执行）：**

1. `internal/model/model.go` 只有五个共享数据类型：`GeoPoint`、`Point2D`、`Rider`、`Order`、`Assignment`。`internal/geo/geo.go` 已有坐标校验、`BoundingBox`、固定的 San Francisco 范围与 `Projector`。先将两个坐标类型和这些地理行为收在 `domain/geo`，使其不再反向引用旧 `model`；保留经纬度边界、投影原点、地球半径和米制坐标算法。
2. 再让 `domain/order` 的 `Order` 与 `domain/rider` 的 `Rider` 引用 `domain/geo` 的坐标类型。`Order.ID` 是订单标识，`Sequence` 是本轮到达顺序，`PlannedArrivalNs` 是模拟计划到达的相对时间；三者不可合并。`Rider.UID`、真实位置和投影点保持原样。本轮不增添订单状态、骑手在线状态或容量状态。
3. `Assignment` 的唯一归属确定为 `domain/matching`。它在当前 demo 中表示算法已选中的匹配结果，包含订单 ID、Sequence、骑手 UID 和**距离平方**；不是未来“骑手接单确认”的记录。`Candidate`、同距按较小 UID 优先的比较、Top-K 保留与排序规则也归 `domain/matching`。
4. 当前 `matcher.DistanceSquared` 和 `matcher.IsFinitePoint` 只处理二维坐标，适合随坐标值归 `domain/geo`；`CopyAndValidateRiders` 是两种空间实现共用的骑手集合校验和复制规则，可暂由 `domain/matching` 提供，避免在两种实现中各写一份。构建后拥有静态骑手快照这一点要保留。
5. 最后迁移 `bruteforce` 与 `kdtree` 两种具体实现到 `adapter/spatial/`。二者都保留 `New`、`Match`、`TopK`、`TopKInto` 的现有能力和错误边界；`TopKInto` 的目标切片复用、Top-K 顺序以及 KD-tree 构建后只读的特性不随目录变化。`matchrun.createMatcher` 目前决定选哪一种实现，选择和装配将在后续步骤四处理。

**需同步更新的直接调用方：**`generator` 使用坐标、订单和骑手；`pipeline` 使用订单、骑手、候选和匹配结果；`report` 使用骑手和匹配结果；`deferred` 使用订单；`matchrun` 使用全部基础类型和搜索器；`matcherapi` 与 CLI 使用这些类型生成对外输出。迁移旧 `model`/`matcher` 路径时应按这些调用方逐层切换，不复制同名领域类型来维持两套模型。

**暂不作为领域规则搬运的类型：**`matcher.IndexStats` 描述具体索引的内存与条目数，是运行报告数据，不是匹配决策规则。两种搜索器现在都返回它，`matchrun.Result`、HTTP 和 CLI 都读取它；应在步骤四确定运行结果与搜索接口的归属时一并迁移，期间保持字段和 `kind` 值不变。

**步骤二执行结果（2026-10-25）：**坐标值、投影、距离计算已收归 `internal/domain/geo`；订单、骑手和算法匹配结果分别归 `internal/domain/order`、`internal/domain/rider`、`internal/domain/matching`。候选排序、同距按较小 UID 优先和骑手快照校验已迁到 `domain/matching`。两种搜索实现及其测试已移到 `internal/adapter/spatial/bruteforce`、`internal/adapter/spatial/kdtree`；生成器、流水线、报告、Deferred、运行编排与两个入口的直接引用已切换。`IndexStats` 暂留原 `internal/matcher` 包等待步骤四处理。`go build ./...` 与两种搜索实现的测试通过；全量测试中 `pipeline` 测试原有的 `RunBalanced` 参数数量不匹配、`generator.TestArrivalModels` 原有的到达时间断言失败，均非此次迁移引入。

### 步骤 3：迁移生成器与 Deferred 存储

- `internal/generator` 按现有职责拆到 `internal/adapter/simulation/orders` 与 `internal/adapter/simulation/riders`；保留 seed、到达模型、空间分布及流式生成行为。若两个生成器共享随机种子或边界初始化，只抽取必要的共享部分，避免产生两套不一致的 seed 规则。
- `internal/deferred` 的 JSONL 读写迁到 `internal/adapter/deferred/file`，内存读写迁到 `internal/adapter/deferred/memory`。保留记录格式、轮次检查和资源关闭责任。
- `OrderSource`、`DeferredOrderSink` 等接口由使用它们的应用层持有；具体适配器实现这些接口。不要让应用流程直接依赖文件路径或内存容器类型。

**步骤三执行结果（2026-10-25）：**`internal/adapter/simulation` 保留边界、投影、独立 seed 和共享空间采样；`orders` 提供到达模型与流式订单源，`riders` 提供骑手生成。原有 `generator` 已移除，调用方改用同一场景对象装配两个生成器。Deferred 的 JSONL source/sink、记录格式迁到 `internal/adapter/deferred/file`，内存 source/sink 迁到 `internal/adapter/deferred/memory`，原有 `deferred` 包已移除。`OrderSource`、`DeferredOrderSink`、`DeferredOrder` 与原因类型由 `internal/app/matching` 持有；现有流水线和运行入口暂时引用此契约，待步骤四迁移流程实现。文件 source 仍由运行编排关闭，文件 sink 仍由 CLI 关闭；内存 sink 保留快照复制和限额行为。

**验证：**`go build ./...`、空间搜索测试及新增的 JSONL 往返/内存快照测试通过。迁移后的生成器测试保留原有 `TestArrivalModels` 断言，仍因第 5 笔 burst 计划时间为 1.6 秒而非预期的 2 秒失败；原实现与测试已有此不一致。`pipeline` 原有测试调用 `RunBalanced` 时缺少 sink 参数，仍无法编译；这两处行为或测试修正不属于步骤三。

### 步骤 4：拆分 `pipeline` 和 `matchrun`

`internal/pipeline` 混合了有界队列、并行计算、按序决策、策略选择、Deferred 和性能统计，不应整包改名。按现有行为拆为：

1. `internal/app/matching`：保留 nearest/balanced 流水线、候选并行查询、有界队列与背压、按 Sequence 决策，以及容量选择。保持“并行查询候选、串行决定骑手”的确定性。
2. `internal/app/run`：承接 `internal/matchrun` 的一次运行编排，包括参数校验、输入准备、生命周期、运行结果和资源收束。运行用例依赖窄接口；具体搜索器、订单源和 Deferred 存储由入口装配。
3. `internal/app/run/replay`：承接现有 Deferred 订单的**整轮**重放。它继续使用相同的运行和匹配能力，不引入拒单后的单笔重派。
4. `internal/app/report`：承接 `internal/report` 的累加、汇总和 Bottom 10；迁移期间保持当前统计口径。流水线中仅属于报告采样与耗时汇总的内容随之整理到应用报告职责，不改计算结果。

处理 `matchrun/runner.go` 时，把算法实现选择、生成器创建、文件源创建等具体装配与运行用例区分开；两个入口仍调用同一套运行能力。不要提前抽象未出现的第三种入口。

**步骤四执行结果（2026-10-25）：**原 `pipeline` 的 nearest/balanced、有界队列、并行候选查询、按 Sequence 决策与容量选择已迁至 `internal/app/matching`；原 `report` 的累加、Bottom 10 和延迟直方图已迁至 `internal/app/report`，索引统计 `IndexStats` 也由报告层持有，字段与 `kind` 值未变。原 `matchrun` 的 `Request`、`Result` 和运行生命周期已迁至 `internal/app/run`。运行用例通过 `InputFactory` 接收准备好的骑手、订单源与静态搜索器；`internal/adapter/simulation/runsetup` 承担原 runner 中的具体生成器、brute-force/KD-tree 和文件 Deferred source 装配。CLI 与现有 HTTP Handler 都注入同一工厂。整轮 Deferred 重放的下一轮参数准备已收归 `internal/app/run/replay`，由原 Retry Handler 调用；单实例会话判断仍留待步骤五。原 `pipeline`、`report`、`matchrun` 和仅剩统计类型的 `matcher` 包已移除。

**步骤四验证：**`go build ./...`、`go test -skip TestArrivalModels ./...` 通过；新运行测试覆盖 nearest/balanced 的分配与 Deferred 数量，新 HTTP 测试覆盖同步 Run/Retry、RunID、轮次与结果字段。已把迁移后的 balanced 测试补齐原本缺失的 sink 和 Attempt 参数，使并行匹配测试能够执行。全量测试仍只有步骤三已记录的生成器 `TestArrivalModels` 预期值与现有到达算法不一致，此步保持算法行为不变。

### 步骤 5：迁移配置、资源监控和单实例会话

- `internal/config` → `internal/platform/config`：保留默认参数和现有校验；把 CLI 参数解析与 HTTP 请求映射的边界分清，避免应用层依赖命令行 flag。
- `internal/resource` → `internal/platform/metrics`：只迁移已有的 Go 运行时采样和汇总能力，不补建空的日志、追踪目录。
- `matcherapi.Handler` 中的 `runSlot`、`lastSession`、RunID 和轮次状态 → `internal/adapter/session/memory`，或由 `app/run` 中的单实例会话对象管理。保留当前“同时只运行一场”和 Retry 对 RunID、来源轮次、Deferred 集合的判断；不把它误写成跨实例存储。

**步骤五执行结果（2026-10-25）：**原 `internal/config` 的运行配置、默认值、显示配置和校验已迁至 `internal/platform/config`；`flag` 解析及对应入口测试移至 `cmd/matcher`，HTTP 请求映射继续留在现有 `matcherapi`，等待步骤六。原 `internal/resource` 的 Go 运行时采样和统计移至 `internal/platform/metrics`，采样字段与 JSON 名称未变。`internal/adapter/session/memory` 现持有单实例运行槽、最新 RunID、轮次、Deferred 集合及 Retry 状态判断；现有 HTTP Handler 仍按原错误状态码与文案映射，轮次响应 DTO 通过泛型存储而未变为会话包的业务类型。运行成功后才保存新会话，Retry 成功后才推进轮次；该状态只在本进程内有效。

**步骤五验证：**`go build ./...`、`go test -skip TestArrivalModels ./...` 通过；配置入口、资源监控、会话运行槽与 Retry 各状态、HTTP Run/Retry 测试通过。CLI 小规模运行的 JSON 中 `riderCount=2`、`completedOrderCount=3`。全量测试仍会触发步骤三已记录的 `TestArrivalModels` 到达时间断言不一致，本步骤未修改到达算法或测试预期。

### 步骤 6：迁移 HTTP，接回两个入口

将 `internal/matcherapi` 的请求 DTO、响应 DTO、字段映射、输入校验和 Handler 移到 `internal/transport/http/run`。保持原有 JSON 字段、错误响应、状态码、路由及同步调用。随后由 `cmd/matcherapi/main.go` 装配新包，仍提供 `/matcher/run` 与 `/matcher/retry`。

再让 `cmd/matcher/main.go` 使用迁移后的运行能力，保留 CLI 参数和现有 JSON 输出。两个入口若确有重复装配，可在都接通后提取少量共享装配；`cmd` 仍负责启动和依赖连接，不承载匹配规则。

### 步骤 7：收尾

逐层切换引用：目标包接管责任后，再移除相应旧包。检查是否存在重复的订单、骑手、候选或结果类型、仅用于转发的旧包、循环依赖，以及应用层对具体适配器的反向依赖。最终只留下本轮有实现的 `domain`、`app`、`adapter`、`platform`、`transport/http` 子目录；后续架构目录等对应功能开始时再建立。
