# 普通聊天 Agent

选定参考：[cloudwego/eino-examples/quickstart/chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)。

此目录实现普通聊天 Agent 的平台适配。沿用 Eino 的执行与 Checkpoint/Resume 架构，启用聊天、只读设置和可选的官方 DuckDuckGo v2 搜索工具，不开放 shell、文件、文档 RAG 或审批。公网搜索来自 eino-ext 官方组件；当前 chatwitheino 的文档 RAG 并不等同于公网搜索，本项目没有因此迁移到另一套 Agent 内核。

- `agent.go`：创建 Eino ChatModelAgent 和独立 Execution，注入可选搜索依赖，转换消息与工具事件。
- `prompt.go`：根据搜索开关组装工具使用、引用来源与失败反馈规则。
- `runner.go`、`checkpoint_store.go`：适配共用 einoruntime 的 TurnLoop、流式事件、安全停止、断点包络和恢复。
- `repository_store.go`：将框架 Checkpoint Store 接入平台 Repository。

真实模型和安全工具边界恢复已验收；强制截流没有有效断点时标记 interrupted，不保证任意 Token 位置续写。证据见项目根目录 docs/real-model-acceptance.md 和 docs/verification.md。

Agent 运行数据通过 Store 适配到平台 Repository；展示历史与模型上下文分离。平台管理 Run 生命周期，Eino 管内部执行与恢复。具体实现与验证以 OpenSpec 变更及实施计划为准。

`WithWebSearch` 在构造时注入 WebSearcher；nil 不注册工具。设置工具遵循 Tool → Service → Repository，搜索遵循 Tool → Client → 官方组件。工具校验、期限与观察事件位于 agenttools，固定端点、原始响应资源释放、结果整理和可选代理位于 client/websearch。

`WEB_SEARCH_ENABLED` 默认 true，`WEB_SEARCH_PROXY_URL` 可选且只作用于搜索。提示/契约/组件版本和开关参与指纹，变化会拒绝旧不兼容断点。查询失败交还模型说明原因，不自动伪造结果或切换供应商；搜索资料是参考内容，不能覆盖系统指令，也不代表抓取过网页正文。

当前公网验收包含一次跑通和后续 DuckDuckGo 验证页面拒绝，不能宣称搜索稳定可用。完整证据与 token/耗时见[验收记录](../../../../../docs/general-web-search-verification.md)。
