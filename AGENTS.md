## 项目级管理后台协议（完整版 — task/admin_plan.md 阶段 6 #44b）

本模块如果要在「项目级管理后台」露出菜单和视图，**必须**维护根目录的 `admin-web.yaml` 种子文件。

### Always Do

- 新增 `/admin/*` 接口时同步更新 `admin-web.yaml`：菜单 + 关键字段 + 危险操作清单
- `admin-web.yaml` 里引用的 `endpoint_key`（stats.api / dangerous）必须能在 `api-docs.json` 里找到对应接口
- 危险操作（封号 / 删数据 / 改配置等）必须列入 `dangerous`，平台会自动加二次确认 + 强制 ctx.Audit 记录 before/after
- 完整规范见 `docs/admin-web-yaml-spec.md`（位于 workspace 根目录）

### Never Do

- 不要在仓库里手写完整 admin spec（菜单 / 视图 / 列定义全配置）—— 那是 AI 在 admin-server 端生成的，仓库只放种子（意图）
- 不要在写操作响应里塞 `_audit` 字段 —— 用 `ctx.Audit(action, before, after, extra)` 上报 diff（跟 ctx.Push / ctx.RegisterWorker 同模式）
- 不要绕开 `ctx.Audit`，自己手写 `INSERT INTO admin_audit_log` —— 那是 runtime middleware 的责任

### 如何让管理后台菜单里出现本模块

`main.go` 在 package 声明后保留这两行（模板默认已有）：

```go
import _ "embed"

//go:embed admin-web.yaml
var AdminWebHint string
```

runtime 加载 .so 时 `plugin.Lookup("AdminWebHint")` 拿到 yaml 内容，解析后聚合到 `/admin/_meta/menu`。

**不想接入管理后台**（如纯后台数据模块）：删掉 `admin-web.yaml` 文件 + 删掉 main.go 的 embed 两行，runtime 静默跳过，菜单里不显示该模块。

### 写操作必须调 ctx.Audit 上报 before / after

新增 admin 接口的写操作（POST/PUT/DELETE/PATCH）handler 必须遵循：

1. 修改前先 SELECT 拿 `before` 对象
2. 执行修改
3. 修改后再 SELECT 拿 `after` 对象
4. 调用 `ctx.Audit("update_xxx", before, after, extra)` 上报 diff

runtime 异步把 diff flush 到 admin-server 的 `admin_audit_log` 表。没调 Audit 也能跑（runtime mutation middleware 会落基础审计：谁、何时、什么接口），但 before/after 字段会空。

**高危操作（admin-web.yaml `dangerous` 列表里的接口）调 ctx.Audit 是硬性要求** —— runtime 还会拒绝请求里没有 `X-Admin-Confirm` 头的调用，前端二次确认弹窗自动塞该 header。

### 本地预览 CLI

```bash
python3 <workspace>/scripts/admin-web-preview.py
```

在模块仓库根目录跑。浏览器打开 `http://localhost:3333` 实时预览菜单、统计卡、危险操作清单。改 `admin-web.yaml` 后刷新页面即时生效，不用部署也不用编译。

### 平台是如何渲染你的模块的

1. 模块部署 → admin-server 编译 .so + 读 `admin-web.yaml`（你写的种子文件）
2. admin-server 调 AI 生成器 → 拿到完整 spec（菜单、视图、列、按钮、过滤器全配齐）落 `module_admin_specs` 表
3. 项目级管理后台前端 → 拉 `/admin/_meta/menu`（runtime 聚合的菜单结构）+ 拉 `module_admin_specs`（具体视图）
4. 用户在 UI 上点列表/详情/操作 → 前端调你模块的 `/admin/api/...` 接口拿真值数据
5. 运营在 UI 上点「重新生成」按钮 → admin-server 重跑 AI 生成 + 落新版本，可回滚

你只需要管两件事：**写好 admin 接口** + **维护 admin-web.yaml**。
