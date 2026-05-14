# 测试用例 — 接口为单位的真测试

本目录下每个 `<api-name>.test.json` 是一个 HTTP 接口的全部测试用例。
平台部署完成后会自动调用模块的 `POST /_internal/selftest` 端点，模块
读取本目录下所有 `.test.json` 文件，逐个执行用例并返回结果。

## 文件命名规则（机器要对齐）

- 一个文件对应一个 API（同 path 不同 method 可以放一起）
- 文件名 = 路径最后一段，如 `POST /api/user/register` → `register.test.json`
- 文件名扩展必须是 `.test.json`

## 文件格式

```json
{
  "api": "POST /api/<module>/<path>",
  "cases": [
    {
      "name": "用例描述（中文 ok）",
      "method": "POST",                         // 可选；默认从 api 字段拆
      "path": "/api/<module>/<path>",           // 可选；默认从 api 字段拆
      "headers": {"X-User-ID": "123"},          // 可选；按需加
      "body": {"username": "test_xx"},          // 可选；POST/PUT body
      "expect_status": 200,                     // 可选；HTTP code
      "expect_field": {                         // 可选；按 . 路径取嵌套字段比对
        "status": 0,
        "data.uid": "123"
      },
      "expect_contains": "ok"                   // 可选；响应体包含子串
    }
  ]
}
```

## 生命周期硬规则（不要变成历史遗留文件）

- 新增 API → **必须**新增对应 .test.json 并覆盖正常 + 异常路径
- 改 API 参数 / 响应 / 错误码 → **必须**同步改 .test.json
- 改 API 路径 → **必须**改文件名 + api 字段 + 内容
- 删除 API → **必须**删除对应 .test.json（cross review 阶段会被机器扫到孤儿，未删则审查不过）

## 用例覆盖度要求

每个 API 至少 1 个正常路径 + 1 个异常路径（如缺参数 / 参数越界 / 业务错误码）。

## 数据隔离 / 模板变量

- 用例可能被反复跑（重跑、修复后再跑），**测试数据用唯一前缀**避免冲突
- 内置变量（path / headers / body / expect_contains / expect_field 的 string value 都会替换）：

| 占位 | 替换为 |
|---|---|
| `${ts}` | 当前秒级时间戳（10 位） |
| `${ts_ms}` | 当前毫秒时间戳（13 位） |
| `${rand}` | 6 位 hex 随机串 |
| `${rand:N}` | N 位 hex 随机串（N=2~32） |
| `${uuid}` | 32 位 hex（不带横线） |

- **同一 case 内同名占位拿到相同值**：`body.phone="138${rand:8}"` 和 `expect_field."data.phone"="138${rand:8}"` 替换后是同一个手机号，断言才能过
- 跨 case 之间各自重新生成
- 测试产生的数据由模块自己负责清理（在 handler 里实现 / 或在用例最后调清理 API）
- 数据库定期任务也会清理 `test_*` / 时间戳前缀的脏数据兜底

例：

```json
{
  "api": "POST /api/user/register",
  "cases": [
    {
      "name": "注册成功",
      "body": {"phone": "138${rand:8}", "code": "1234"},
      "expect_status": 200,
      "expect_field": {"status": 0, "data.phone": "138${rand:8}"}
    }
  ]
}
```

## 执行触发方式

| 触发 | 何时 |
|---|---|
| 自动 | 每次 deploy 完成后由 admin-server 调用 |
| 手动 | 前端面板"重跑测试"按钮 |
| 不再支持 `go test -v ./...` 跑这些 | go test 只测单元逻辑；这里是端到端 |

## 安全

- `/_internal/selftest` 端点要求 `X-Internal-Token` header 匹配 `RUNTIME_INTERNAL_TOKEN` 环境变量
- 外部请求一律 401
- panic 兜底：单个 case panic 算单个失败，不影响其它 case
