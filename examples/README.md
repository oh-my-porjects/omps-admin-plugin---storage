# 示例

## 最小插件结构

插件入口在根目录 main.go，核心要求：

1. 导出 Plugin 符号（名称必须为 Plugin）
2. 实现 GamePlugin 接口（Name, Version, Init, Shutdown）
3. `Version()` 返回包级 `var version`，admin-server 编译期通过 ldflags 注入

## 常见修改点

| 修改点 | 文件 | 说明 |
|--------|------|------|
| 插件逻辑 | main.go | Init / Shutdown 中编写业务代码 |
| 插件名称 | main.go + plugin.yaml | 通常不需要改 |

## 验证步骤

```bash
# 运行测试
go test -v ./...

# 编译为插件（本地验证）
CGO_ENABLED=1 go build -buildmode=plugin -o module.so .
```
