# 文件存储 (storage)

## 功能
负责将业务图片上传到 Cloudflare R2，并维护资源元数据、归属用户和业务对象绑定关系。

## 接口

| 方法 | 路径 | 说明 | 鉴权 |
|--------|------|-------------|------|
| GET | /api/storage/hello | 返回模块名称和版本 | public |
| POST | /api/storage/upload | 上传 JPG、PNG、WEBP 图片并保存资源记录 | login |
| GET | /api/storage/admin/resource-list | 后台分页查询资源列表 | api_key |
| GET | /api/storage/admin/resource-detail | 后台查询资源详情 | api_key |
| POST | /api/storage/admin/ping | 管理后台探活 | api_key |
| POST | /_internal/method-call/storage | Runtime 内部方法调用入口 | api_key |
| POST | /_internal/scheduled-trigger/storage | Runtime 内部定时任务触发入口 | api_key |
| POST | /_internal/selftest/storage | 已废弃的内部自测入口 | api_key |

## 数据库

- `storage_resources` — 保存图片资源元数据；关键字段包括 `id` 主键、`upload_batch_id` 批次、`is_current` 当前有效标记、`user_id` 归属用户、`feature` 业务用途、`business_object_type/business_object_id` 绑定对象、`storage_key` 唯一对象键、`public_url` 访问地址、`status` 生命周期状态；约束包括 `file_size_bytes > 0`、`status` 只能为 `normal/deleted/cleaned`、`storage_key` 唯一，并按用户、用途、业务对象、批次和状态建索引。

## 设计说明

- 上传只接受服务端按文件内容识别出的 JPG、PNG、WEBP，避免仅依赖文件扩展名导致错误格式入库。
- 当前上传用途白名单只有 `recharge_voucher`，业务对象只允许 `recharge_order` 和 `user_profile`，用于限制公共存储能力被任意场景滥用。
- 同一 `feature + business_object_type + business_object_id` 只保留一个当前有效批次，新上传或内部绑定会把旧资源置为非当前，历史记录需后台显式查询。
- 删除采用软删除和延迟清理两段式：内部方法置为 `deleted`，清理任务按全局保留期删除 R2 对象后置为 `cleaned`。
- 代码中定义了内部绑定、软删除和清理任务注册函数，但当前未在初始化流程调用；内部入口存在，具体方法和任务默认未注册。

## 环境变量

- `STORAGE_R2_ACCOUNT_ID` — Cloudflare R2 账号 ID，无默认值
- `STORAGE_R2_ACCESS_KEY_ID` — Cloudflare R2 Access Key ID，无默认值
- `STORAGE_R2_SECRET_ACCESS_KEY` — Cloudflare R2 Secret Access Key，无默认值
- `STORAGE_R2_BUCKET` — Cloudflare R2 bucket 名称，无默认值
- `STORAGE_R2_PUBLIC_BASE_URL` — 资源公网访问域名，无默认值
- `STORAGE_ENVIRONMENT` — 对象存储路径中的环境隔离前缀，默认 `dev`

## 依赖模块

- `global-config` — 清理逻辑读取 `global_configs_items.storage.resource_delete_retention_days`；缺失或非法时按 7 天处理。

## 被依赖模块

- 无
