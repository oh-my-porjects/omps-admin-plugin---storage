package main

// 模块定时任务手动触发入口（POST /_internal/scheduled-trigger/storage）
//
// 由 admin-server 透过 runtime 透传过来，用于在 admin-web 测试页手动触发某个
// 定时任务并流式返回执行日志。
//
// 请求体：{ "task_name": "cleanup_expired_sessions" }
// 响应：text/event-stream 流（每行一个 data: <log line> event）
//
// 模块开发者：在 init() 里把任务注册到 ScheduledTasks 表
//   key = 任务名 → ScheduledTaskFunc(emit func(string)) error
// 任务函数通过 emit 输出实时日志，平台前端实时显示

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// ScheduledTaskFunc 任务签名：用 emit 写日志，返回 error 表示失败
type ScheduledTaskFunc func(emit func(line string)) error

// ScheduledTasks 全局任务表，模块开发者在 init 时注册
var ScheduledTasks = map[string]ScheduledTaskFunc{}

// 防止同一任务并发触发
var triggerMu sync.Mutex

func init() {
	Routes["POST /_internal/scheduled-trigger/storage"] = handleScheduledTriggerInternal
}

func handleScheduledTriggerInternal(w http.ResponseWriter, r *http.Request) {
	expect := os.Getenv("RUNTIME_INTERNAL_TOKEN")
	if expect == "" || r.Header.Get("X-Internal-Token") != expect {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		TaskName string `json:"task_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.TaskName == "" {
		http.Error(w, "task_name 不能为空", http.StatusBadRequest)
		return
	}
	fn, ok := ScheduledTasks[req.TaskName]
	if !ok {
		http.Error(w, "未注册的定时任务: "+req.TaskName, http.StatusNotFound)
		return
	}

	// SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)

	emit := func(line string) {
		fmt.Fprintf(w, "data: %s\n\n", line)
		if flusher != nil {
			flusher.Flush()
		}
	}

	triggerMu.Lock()
	defer triggerMu.Unlock()

	emit(fmt.Sprintf("[%s] 任务 %s 开始执行", time.Now().Format(time.RFC3339), req.TaskName))
	defer func() {
		if rec := recover(); rec != nil {
			emit(fmt.Sprintf("[ERROR] panic: %v", rec))
		}
	}()
	startedAt := time.Now()
	err := fn(emit)
	elapsed := time.Since(startedAt)
	if err != nil {
		emit(fmt.Sprintf("[%s] 任务失败: %v（耗时 %s）", time.Now().Format(time.RFC3339), err, elapsed))
	} else {
		emit(fmt.Sprintf("[%s] 任务完成（耗时 %s）", time.Now().Format(time.RFC3339), elapsed))
	}
}
