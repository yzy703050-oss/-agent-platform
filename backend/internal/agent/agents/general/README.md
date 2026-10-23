# 普通聊天 Agent

选定参考：[cloudwego/eino-examples/quickstart/chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)。

此目录当前只有职责说明，尚未实现。沿用示例的执行与 Checkpoint/Resume 架构，首版只启用聊天和只读工具，不开放 shell、文件、文档 RAG 或审批。

Agent 运行数据通过 Store 适配到平台 Repository；展示历史与模型上下文分离。平台管理 Run 生命周期，Eino 管内部执行与恢复。具体实现与验证以 OpenSpec 变更及实施计划为准。

