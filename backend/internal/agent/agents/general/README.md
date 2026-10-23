# 普通聊天 Agent

选定参考：[cloudwego/eino-examples/quickstart/chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)。

此目录已实现普通聊天 Agent 的平台适配。沿用示例的执行与 Checkpoint/Resume 架构，首版只启用聊天和只读设置工具，不开放 shell、文件、文档 RAG 或审批。

- `agent.go`：创建 Eino ChatModelAgent 和独立 Execution，转换消息与工具事件。
- `runner.go`：封装 TurnLoop、流式事件消费、安全停止和恢复。
- `checkpoint_store.go`：校验断点版本、配置指纹和保存安全性。
- `repository_store.go`：将框架 Checkpoint Store 接入平台 Repository。

真实模型和安全工具边界恢复已验收；强制截流没有有效断点时标记 interrupted，不保证任意 Token 位置续写。证据见项目根目录 docs/real-model-acceptance.md 和 docs/verification.md。

Agent 运行数据通过 Store 适配到平台 Repository；展示历史与模型上下文分离。平台管理 Run 生命周期，Eino 管内部执行与恢复。具体实现与验证以 OpenSpec 变更及实施计划为准。
