package main

// selftest.go — 模块自测端点（POST /_internal/selftest/storage）
//
// **已废弃**：流水线不再调用本端点，admin-server 端的 apiTestPhase 直接读模块
// tests/*.test.json 自己跑（见 admin-server scheduler_apitest_executor.go）。
//
// 废弃原因：旧实现用 httptest.NewRequest 进程内直接调 handler，绕过了 actor 鉴权
// 注入、{range_tag} 占位真实替换、tests/data.yaml 种子数据灌入。结果：
//   - 后台账号 / 用户登录类需要 X-API-Key / X-User-ID 鉴权的接口必然 401
//   - banned_user / 测试管理员等用例查空库失败
//   - expect_business_status 等高级期望字段被无视
// AI 看到的 fail_reason 都是表层"401 / 字段不等"，反复 fix 越改越烂。
//
// 端点本体保留为 410 提示，让任何残留调用方一眼看到该走 admin-server。
// 模块开发者本机想自测单接口，直接 curl runtime 8080 业务端口即可（带上
// X-API-Key / X-User-ID 真值）。

import (
	"net/http"
	"os"
)

func init() {
	Routes["POST /_internal/selftest/storage"] = handleSelftestInternal
}

func handleSelftestInternal(w http.ResponseWriter, r *http.Request) {
	// 内部 token 仍然校验，避免没鉴权就告诉外部"这模块叫什么"
	expect := os.Getenv("RUNTIME_INTERNAL_TOKEN")
	if expect == "" || r.Header.Get("X-Internal-Token") != expect {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// 410 Gone：明确告诉调用方端点已废弃 + 替代方案在哪
	writeJSON(w, 1, nil,
		"selftest 端点已废弃；流水线 API 自动测试改由 admin-server 直接读 tests/*.test.json 执行（见 scheduler_apitest_executor.go）。"+
			"开发者本机自测单接口请直接 curl runtime 8080 业务端口并带上 X-API-Key / X-User-ID。")
}
