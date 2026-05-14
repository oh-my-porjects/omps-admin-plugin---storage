# 模块

模块是项目中的独立功能单元。每个模块是一个独立的 Git 仓库，编译为 `.so` 插件，由 Runtime 动态加载。

项目的业务代码全部在模块中，主项目仓库不包含业务代码。

模块按系统级划分（如 user、wallet、payment），不按功能级划分（不要拆成 user-register、user-login）。

## 模块划分规则

三条规则，按项目阶段依次使用：

### 1. 部署独立性（从零规划时用）

改了 A 功能后，如果 B 功能不用重新编译部署就能正常工作，A 和 B 就是两个模块。反过来，如果改了注册逻辑，登录也必须跟着重新编译才能正常工作，说明注册和登录是同一个模块。

例：user（注册+登录+资料）是一个模块，wallet（余额+流水）是另一个模块，因为改用户逻辑不需要重新部署钱包。

### 2. 事务边界（设计阶段用）

如果两个操作必须要么同时成功、要么同时失败（比如扣库存和创建订单），它们必须在同一个模块里。如果两个操作各自成功失败互不影响（比如充值和提现），就可以拆成两个模块。

### 3. 写权限归属（开发阶段用）

每张表只有一个模块能写。`user_profiles` 表只有 user 模块能 INSERT/UPDATE/DELETE，其他模块可以 SELECT 读取，但要修改用户数据必须调 user 模块的接口。这样避免多个模块同时写一张表导致数据混乱。

## 仓库结构

```
├── main.go              # 插件入口，导出 Plugin 符号（必须）
├── main_test.go         # Go 单元测试（自动校验接口规范）
├── selftest.go          # 自测端点框架（POST /_internal/selftest，admin-server 部署后调）
├── plugin.yaml          # 模块元数据（名称、版本、描述）
├── go.mod               # Go 模块定义
├── examples/
│   └── README.md        # 最小示例和常见修改点
├── tests/               # API 端到端测试用例（按接口为单位的 JSON 文件）
│   ├── README.md        # 测试用例文件格式 + 生命周期硬规则
│   └── *.test.json      # 每个 API 一个文件，dev/fix 阶段同步维护
└── .gitignore
```

## 测试体系（两层）

| 层 | 跑什么 | 何时跑 |
|---|---|---|
| Go 单元测试（main_test.go + 业务 _test.go） | 内部逻辑、工具函数 | dev 阶段 `go test -v ./...` |
| API 端到端用例（tests/*.test.json） | 真实 HTTP 调模块对外 API | admin-server 部署完自动调 `/_internal/selftest`；前端"重跑测试"按钮也调它 |

API 端到端用例**按接口为单位**：每个 API 一个 .test.json 文件。增删改 API 必须同步增删改对应文件，否则交叉验证阶段会被机器扫到孤儿/缺失，要求修复。详见 `tests/README.md`。
```

## 插件接口规范

Runtime 通过 `plugin.Lookup("Plugin")` 加载模块，要求：

```go
// 1. 必须导出名为 Plugin 的变量
var Plugin = &MyPlugin{}

// 2. 必须实现 GamePlugin 接口（v2, 2026-04-20）
type MyPlugin struct{}

// version 由 admin-server 在编译期通过 ldflags 注入；本地测试时是 "dev"
var version = "dev"

func (p *MyPlugin) Name() string                            { return "my-module" }
func (p *MyPlugin) Version() string                         { return version }
func (p *MyPlugin) Init(ctx PluginContext) error            { return nil }
func (p *MyPlugin) Shutdown(ctx context.Context) error      { return nil }
```

| 方法 | 说明 | 注意事项 |
|------|------|----------|
| Name() | 返回模块名称 | 必须与 plugin.yaml 的 name 字段一致 |
| Version() | 返回版本号 | 由 admin-server 编译期 ldflags 注入，模块作者无需维护 |
| Init(ctx) | Runtime 加载模块后调用 | ctx 为 PluginContext，提供 DB/Config/Logger/LifecycleCtx/RegisterWorker/IsUnloading |
| Shutdown(ctx) | Runtime 卸载模块时调用 | ctx 携带超时；LifecycleCtx 在此之前已 cancel，后台 worker 应已退出 |

### PluginContext 字段

```go
type PluginContext struct {
    DB             *sql.DB           // 共享连接池
    Config         map[string]string // 环境配置
    Logger         *slog.Logger      // 带模块名前缀的日志
    LifecycleCtx   context.Context   // Reload/Stop 开始时 Done()，后台 worker 必须监听此信号
    RegisterWorker func() func()     // goroutine 启动前调用，返回 dereg 函数供 defer
    IsUnloading    func() bool       // 查询模块是否处于 unloading 状态
}
```

### 后台 worker 协作退出（硬性约束）

启动 goroutine 的模块**必须**两件事：
1. 调用 `ctx.RegisterWorker()` 并 `defer` 其返回值，让 Runtime 精确跟踪 active_workers
2. 在 `select` 里监听 `ctx.LifecycleCtx.Done()`，收到信号时退出

```go
func (p *MyPlugin) runTicker() {
    done := p.registerWorker()
    defer done()
    t := time.NewTicker(time.Second)
    defer t.Stop()
    for {
        select {
        case <-p.lifecycleCtx.Done():
            return
        case <-t.C:
            // 干活
        }
    }
}
```

未做上述两项的模块在 reload/stop 时会泄漏 goroutine，Runtime 的 active_workers 计数会错位，约束引擎的 `background_worker_reload_risk` / `worker_change_without_reload_verification` 规则会 block 部署。

## 版本号机制

模块二进制的版本号由 admin-server 在编译期通过 ldflags 注入，**模块作者完全不需要维护**：

- main.go 里写 `var version = "dev"`，`Version()` 返回这个变量
- admin-server 部署时本地编译 .so，命令带 `-ldflags "-X <module_path>.version=<deploy_tag>"`
- 注入后 .so 里 `Version()` 返回本次部署的 deploy_tag，跟 admin-server 校验逻辑严格对齐
- 不再需要每次发版手改源码、不再需要 plugin.yaml 写 version 字段

本地 `go test` 时 `Version()` 拿到默认值 `"dev"`。

## 编译与测试

```bash
# 运行测试
go test -v ./...

# 编译为插件（本地手动编译只用于开发自测；生产由 admin-server 注入版本号编译）
CGO_ENABLED=1 go build -buildmode=plugin -o module.so .
```

测试自动校验：Name() 不为空、Init(ctx)/Shutdown(ctx) 能正常返回。

## 开发流程

1. 在平台创建模块（自动初始化仓库）
2. 修改 main.go，在 Init(ctx)/Shutdown(ctx) 中实现业务逻辑，后台 goroutine 必须遵守协作退出协议
3. 运行 `go test -v ./...` 确认测试通过
4. 提交代码、推送（不需要打 tag、不需要改版本号；admin-server 部署时会自动打 tag 和注入版本号）
5. 在平台选择版本部署

## 数据库

每个项目环境有独立的 PostgreSQL，前面有 PgBouncer 做连接池代理。

**连接架构：**

```
Runtime 实例 A ──┐
Runtime 实例 B ──┤→ PgBouncer (连接复用) → PostgreSQL
Runtime 实例 C ──┘
```

- Runtime 统一管理一个连接池，通过 PluginContext 传给模块
- 模块不自己建连接，直接用 `ctx.DB` 查询
- PgBouncer 负责跨实例的连接复用，横向扩展不用担心连接数

**使用方式：**

```go
type MyPlugin struct {
    db *sql.DB
}

func (p *MyPlugin) Init(ctx PluginContext) error {
    p.db = ctx.DB  // 用 Runtime 提供的共享连接池

    // 建表（如果不存在）
    _, err := p.db.Exec(`CREATE TABLE IF NOT EXISTS battle_rooms (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        name TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'waiting',
        created_at TIMESTAMPTZ DEFAULT NOW()
    )`)
    return err
}

func (p *MyPlugin) Shutdown(ctx context.Context) error {
    // 不关闭 db，由 Runtime 统一管理
    return nil
}
```

**数据库规范：**

- 表名必须加模块名前缀（如 `battle_rooms`、`user_profiles`），同环境所有模块共享一个数据库
- 必须使用参数化查询（`$1, $2`），禁止拼接 SQL 字符串
- 可以新增字段，但禁止删除或修改已有字段（避免破坏其他版本兼容性）
- 建表用 `CREATE TABLE IF NOT EXISTS`，保证多次 Init 幂等
- 查询用 PostgreSQL 语法：自增用 `BIGSERIAL`，占位符用 `$1`，时间用 `TIMESTAMPTZ`

## Redis

每个项目环境有独立的 Redis，Runtime 统一管理连接，通过 PluginContext 传给模块。

**使用方式：**

```go
type MyPlugin struct {
    rdb *redis.Client
}

func (p *MyPlugin) Init(ctx PluginContext) error {
    p.rdb = ctx.Redis  // 用 Runtime 提供的 Redis 客户端
    return nil
}
```

**适用场景：**
- 会话缓存（玩家登录态、临时数据）
- 排行榜（Sorted Set）
- 发布订阅（多 Runtime 实例间消息广播）
- 分布式锁（跨实例资源互斥）
- 限流计数

**Redis 规范：**

- Key 必须加模块名前缀，格式 `模块名:业务:标识`（如 `battle:room:1001`）
- Key 必须设置 TTL，避免内存无限增长
- 避免 `KEYS *`（会阻塞），用 `SCAN` 替代
- 大 Value（>1MB）拆分存储
- 同环境所有模块共享一个 Redis 实例，Key 前缀是唯一的隔离手段

## API 响应规范

所有 API 必须使用统一的响应格式：

```json
{
  "status": 0,
  "data": null,
  "msg": "提示消息"
}
```

- `status`：0 表示成功，非零表示具体错误码
- `data`：响应数据，类型根据接口不同可以是 object/array/string/number/null
- `msg`：提示消息，成功时可为空，失败时返回错误描述

每个 API 必须明确认证方式：
- `login`：需要用户登录后调用
- `api_key`：通过密钥调用
- `public`：公开接口，无需认证

### api-docs.json 嵌套字段写法

doc-writeback 阶段产出的 `api-docs.json` 描述每个接口的请求/响应结构，会被 admin-server 推送到 Apifox。响应里出现嵌套对象或数组对象时，按以下约定写，平台才能完整展开导出：

- 嵌套对象：用 `type:"object" + fields:[...]`，子字段继续按同样规则写
- 数组（items 是对象）：用 `type:"array" + fields:[...]`
- 数组（items 是标量）：用 `type:"array" + item_type:"string"`（或 integer/number/boolean）

最小示例：

```json
{
  "name": "user",
  "type": "object",
  "description": "用户信息",
  "fields": [
    { "name": "user_id", "type": "string" },
    { "name": "profile", "type": "object", "fields": [
      { "name": "nickname", "type": "string" }
    ] },
    { "name": "tags", "type": "array", "item_type": "string" }
  ]
}
```

完整示例见 [examples/api-docs.example.json](examples/api-docs.example.json) 的 `/api/me` 接口。

## 数据库规范

- 主键统一使用 UUID 类型：`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
- 字符串统一使用 TEXT 类型，长度约束在应用层校验
- 表名必须带模块名前缀（如 `battle_rooms`、`user_profiles`）
- 字段只增不删，避免破坏兼容性
- 建表用 `CREATE TABLE IF NOT EXISTS`，保证多次 Init 幂等

## 模块间通信

模块之间不直接依赖，通过 Runtime 插件协议通信。


## 代码规范

- 每个文件按功能分割，粒度越小越好，单文件不超过 500 行
- 原因：AI 读取和理解文件能力有限，小文件更容易被准确理解和修改
