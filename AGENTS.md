# 存储公共模块

- 项目后台 V4 只读取 `admin-intents.yaml`、已验证 `api-docs/` 和项目业务意图；不得恢复 `admin-web.yaml` 或 `AdminWebHint`。
- 资源列表、详情和上传必须使用真实接口合同，任何文件字段必须明确为 multipart/form-data 才能由 runtime 执行。
- 资源 ID、业务对象 ID 和公网地址为真实数据；关联显示需要批量 lookup 合同，禁止前端逐条请求或伪造名称。
