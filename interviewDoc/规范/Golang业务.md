**【Golang业务】项目/模块目录结构规范**

:::info
不仅要“能跑”，还要“可扩展、可观测、可演进、可回溯”。本文从架构设计、工程化、高并发性能优化、代码质量四个维度，对 Go 后端项目进行生产级重构

:::

### 以哔哩哔哩的Kratos微服务框架为例：

├── CHANGELOG.md

├── OWNERS

├── README.md

├── api # api目录为对外保留的proto文件及生成的pb.go文件

│ ├── api.bm.go

│ ├── api.pb.go # 通过go generate生成的pb.go文件

│ ├── api.proto

│ └── client.go

├── cmd

│ └── main.go # cmd目录为main所在

├── configs # configs为配置文件目录

│ ├── application.toml # 应用的自定义配置文件，可能是一些业务开关如：useABtest = true

│ ├── db.toml # db相关配置

│ ├── grpc.toml # grpc相关配置

│ ├── http.toml # http相关配置

│ ├── memcache.toml # memcache相关配置

│ └── redis.toml # redis相关配置

├── go.mod

├── go.sum

└── internal # internal为项目内部包，包括以下目录：

│ ├── dao # dao层，用于数据库、cache、MQ、依赖某业务grpc|http等资源访问

│ │ ├── dao.bts.go

│ │ ├── dao.go

│ │ ├── db.go

│ │ ├── mc.cache.go

│ │ ├── mc.go

│ │ └── redis.go

│ ├── di # 依赖注入层 采用wire静态分析依赖

│ │ ├── app.go

│ │ ├── wire.go # wire 声明

│ │ └── wire_gen.go # go generate 生成的代码

│ ├── model # model层，用于声明业务结构体

│ │ └── model.go

│ ├── server # server层，用于初始化grpc和http server

│ │ ├── grpc # grpc层，用于初始化grpc server和定义method

│ │ │ └── server.go

│ │ └── http # http层，用于初始化http server和声明handler

│ │ └── server.go

│ └── service # service层，用于业务逻辑处理，且为方便http和grpc共用方法，建议入参和出参保持grpc风格，且使用pb文件生成代码

│ └── service.go

└── test # 测试资源层 用于存放测试相关资源数据 如docker-compose配置 数据库初始化语句等

└── docker-compose.yaml

## 工程化升级：让团队可以稳定交付

### 2.1 项目布局（production-ready）

```plain
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

- • 避免把业务暴露在 `pkg/ `，对外只暴露稳定 API。

### 2.2 配置与密钥治理

```plain
type Config struct {
    HTTP struct { Addr string; ReadTimeout time.Duration }
    DB   struct { DSN string; MaxOpenConns int; MaxIdleConns int; ConnMaxLifetime time.Duration }
    Redis struct { Addr string; Password string; DB int }
}

func LoadConfig() (*Config, error) {
    v := viper.New()
    v.SetConfigName("config")
    v.AddConfigPath("./configs")
    v.AutomaticEnv()
    v.SetEnvPrefix("APP")
    v.SetConfigType("yaml")
    if err := v.ReadInConfig(); err != nil {
        return nil, err
    }
    var cfg Config
    if err := v.Unmarshal(&cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

- • **密钥 ** ：从 Vault/Secret Manager 注入环境变量，不写入仓库；配置可热更新配合 `fsnotify ` 。

### 2.3 CI/CD 基线

- • `make lint test build ` 作为统一入口；CI 阶段运行 `golangci-lint ` , `go test -race ` , `govulncheck ` , `go vet ` , 覆盖率门禁；CD 阶段镜像构建 + SBOM。

示例 Makefile 片段：

```plain
lint: ; golangci-lint run ./...
test: ; go test ./... -race -coverprofile=coverage.out
build: ; CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/service ./cmd/service
```

### 2.4 API 契约优先

- • HTTP 用 OpenAPI，gRPC 用 proto；生成 server/client stubs；在 CI 校验契约向后兼容。

### 2.5 数据迁移与回滚

- • 规范使用 `golang-migrate ` ；每个变更自带 `up/down ` ；在发布流水线先跑 `migrate up ` , 失败即回滚。

### 2.6 运行时运维：探活 + 灰度 + Feature Flag

- • `/healthz ` 轻量探活；/readyz 检查依赖连通性。
- • 灰度发布通过 Header / 用户维度路由；功能开关用 `go-feature-flag ` 或自研配置中心。

## 3. 性能与高并发：用数据驱动优化

### 3.1 连接与池化

```plain
sqlDB.SetMaxOpenConns(200)
sqlDB.SetMaxIdleConns(50)
sqlDB.SetConnMaxLifetime(30 * time.Minute)
```

- • HTTP Client 配置 `Transport ` 的连接池；Redis 统一 `*redis.Client ` 。

### 3.2 缓存与一致性

```plain
var singleFlight = singleflight.Group{}

func (r *Repo) GetUser(ctx context.Context, id string) (User, error) {
    if hit, err := r.cache.Get(ctx, id); err == nil { return hit, nil }

    v, err, _ := singleFlight.Do("user:"+id, func() (any, error) {
        u, err := r.db.Find(ctx, id)
        if err != nil { return nil, err }
        _ = r.cache.Set(ctx, id, u, time.Minute)
        return u, nil
    })
    if err != nil { return User{}, err }
    return v.(User), nil
}
```

- • 使用 singleflight 避免缓存击穿；热点 Key 可加本地 LRU；过期用一致性哈希降低失效雪崩。

### 3.3 队列与解耦

- • 写入路径异步化：主流程落日志/事件到 Kafka/NATS，消费侧保证幂等；失败重试 + DLQ；消费者按分区/主题水平扩展。

### 3.4 基于数据的性能迭代

- • 在非生产压测环境做负载曲线：并发 QPS、p99、GC Pause、goroutine 数量。
- • 用 `pprof ` , `tracing ` , `go test -bench ` 找热点，再做针对性优化。

### 3.5 常用高并发模式

```plain
// 有界工作池 + 背压
tasks := make(chan Job, 1024)
wg := sync.WaitGroup{}
for i := 0; i < runtime.NumCPU()*2; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        for job := range tasks {
            if err := job.Do(); err != nil { log.Warn("job failed", zap.Error(err)) }
        }
    }()
}
```

- • **背压 ** ：队列满时直接丢弃/降级；不要无限制 goroutine。

### 3.6 GC 与内存

- • 预分配切片/Map；复用 `sync.Pool ` ；避免 `fmt.Sprintf ` 频繁分配；热点路径用 `[]byte ` + `bytes.Buffer ` ；必要时调优 `GOGC ` ，但优先优化分配。

## 4. 代码质量：可测试、可演进、可审计

### 4.1 错误处理：包装 + 单点日志

```plain
func (s *OrderService) Create(ctx context.Context, req CreateOrderRequest) (_ Order, err error) {
    defer func() {
        if err != nil {
            logger := mustLogger(ctx)
            logger.Warn("create order failed",
                zap.Error(err),
                zap.String("trace_id", mustTraceID(ctx)),
                zap.Any("req", req),
            )
        }
    }()

    if err = validate(req); err != nil {
        return Order{}, fmt.Errorf("validate: %w", err)
    }
    // ...
    return order, nil
}
```

- • 日志只在边界层（Handler / Job Runner）打一次，业务层返回带上下文的错误。

### 4.2 表驱动 + 子测试 + Fuzz + Race

```plain
func TestParseAmount(t *testing.T) {
    cases := []struct {
        name string
        in   string
        want int64
        err  string
    }{
        {"ok", "12.34", 1234, ""},
        {"negative", "-1", -100, ""},
        {"bad", "abc", 0, "invalid"},
    }
    for _, tt := range cases {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseAmount(tt.in)
            if tt.err != "" { require.ErrorContains(t, err, tt.err); return }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}

func FuzzParseAmount(f *testing.F) {
    f.Add("12.34")
    f.Fuzz(func(t *testing.T, input string) { _, _ = ParseAmount(input) })
}
```

- • CI 开启 `go test -race ` 捕获数据竞争。

### 4.3 静态分析与风格

- • `golangci-lint ` 启用 `govet, staticcheck, revive, errcheck, gocognit ` ; 对复杂函数设认知复杂度阈值。
- • 统一日志库（zap/logrus）和错误库（ `errors.Is/As/Join ` ）。

### 4.4 安全基线

- • 任何外部输入必须校验；SQL 使用参数化；HTTP 响应默认 `Content-Type ` ；禁止在日志中打印敏感数据；依赖定期跑 `govulncheck ` 。

### 4.5 可发布文章结构

1.  1.  摘要 & 适用场景
2.  2.  架构蓝图与设计原则
3.  3.  工程化流水线与配置治理
4.  4.  高并发与性能优化手册
5.  5.  代码质量与测试策略
6.  6.  生产检查清单（可打印）
7.  7.  参考资料与落地路线

## 生产检查清单（可直接用）

- • **代码质量 ** ： `go vet ` / `golangci-lint ` / `staticcheck ` 通过；无 panic 滥用；圈复杂度可控。
- • **错误处理 ** ：所有错误包装 `%w ` ；边界层单点日志；敏感信息脱敏。
- • **资源管理 ** ： `io.Closer ` 及时关闭；goroutine 有退出；channel 正确关闭；Context 传递且有超时。
- • ** 可观测性 ** ：结构化日志 + TraceID；关键指标（QPS、p99、错误率、队列积压、goroutine 数）暴露；慢查询告警。
- • ** 性能/并发 ** ：连接池参数设定；缓存策略（穿透/击穿/雪崩）覆盖；重试有退避且与超时协调；压测基线记录。
- • ** 安全 ** ：输入校验、鉴权鉴别、RBAC、SQL 参数化、依赖漏洞扫描、证书轮换计划。
- • ** 发布 ** ：Migration 先行；灰度与回滚剧本；探活/就绪检查通过；开关可快速降级。

---

## 参考与延伸

- • Go 官方文档与 Effective Go
- • Uber Go Style Guide
- • Go Project Layout (社区基线)
- • Principles of Chaos Engineering（验证弹性）
- • Google SRE Workbook（可观测性与容量）
