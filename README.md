# 文件存储 (storage)

## 功能
负责上传业务图片到 Cloudflare R2，并维护图片资源与用户、业务对象、业务用途之间的当前有效绑定关系。

## 接口

| 方法 | 路径 | 说明 | 鉴权 |
|--------|------|-------------|------|
| GET | /api/storage/hello | 返回模块名称和版本 | public |
| POST | /api/storage/upload | 上传 JPG、PNG、WEBP 图片并生成资源记录 | login |
| GET | /api/storage/admin/resource-list | 后台分页查询资源列表 | api_key |
| GET | /api/storage/admin/resource-detail | 后台查询单个资源详情 | api_key |
| POST | /api/storage/admin/ping | 管理后台连通性检查 | api_key |
| POST | /_internal/method-call/storage | Runtime 内部方法调用入口 | api_key |
| POST | /_internal/scheduled-trigger/storage | Runtime 内部手动触发定时任务 | api_key |
| POST | /_internal/selftest/storage | 已废弃的内部自测入口 | api_key |

## 数据库

- `storage_resources` — 保存上传资源元数据；关键字段包括 `id` 主键、`upload_batch_id` 批次、`is_current` 当前有效标记、`user_id` 归属用户、`feature` 业务用途、`business_object_type/business_object_id` 绑定对象、`storage_key` 唯一对象存储键、`public_url` 访问地址、`status` 状态（`normal/deleted/cleaned`）、`uploaded_at/deleted_at/cleaned_at` 时间；约束包括文件大小必须大于 0、状态枚举检查、`storage_key` 唯一索引和按用户/用途/业务对象/批次/状态的查询索引。

## 设计说明

- 上传接口只接受图片，服务端根据文件内容识别 MIME，避免仅信任文件名导致错误格式进入对象存储。
- 当前业务用途只开放 `recharge_voucher`，业务对象只允许 `recharge_order` 和 `user_profile`，用于限制存储模块被任意业务滥用。
- 同一 `feature + business_object_type + business_object_id` 只保留一个当前有效批次；新上传或重新绑定会把旧资源标记为非当前，历史记录仍可由后台显式查询。
- 删除分为软删除和清理：内部方法先把资源置为 `deleted`，定时任务按全局保留期删除 R2 对象后再置为 `cleaned`。
- 生产上传依赖 R2 配置；配置缺失时上传失败，自测请求可通过专用 header 使用本地 mock 路径。

## 环境变量

- `STORAGE_R2_ACCOUNT_ID` — Cloudflare R2 账号 ID，无默认值
- `STORAGE_R2_ACCESS_KEY_ID` — Cloudflare R2 Access Key ID，无默认值
- `STORAGE_R2_SECRET_ACCESS_KEY` — Cloudflare R2 Secret Access Key，无默认值
- `STORAGE_R2_BUCKET` — Cloudflare R2 bucket 名称，无默认值
- `STORAGE_R2_PUBLIC_BASE_URL` — 资源公网访问域名，无默认值
- `STORAGE_ENVIRONMENT` — 对象存储路径环境前缀，默认 `dev`

## 依赖模块

- `global-config` — 定时清理读取 `global_configs_items` 中的 `storage.resource_delete_retention_days` 配置；缺失或非法时默认 7 天。

## 被依赖模块

- 无
