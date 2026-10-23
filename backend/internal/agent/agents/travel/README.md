# Go 原生差旅 Agent

实现平台 contracts.Agent，共享 Run、身份、SSE、页面租约、Eino Runner 和 Repository 断点。General 与 Travel 独立入口，主 Agent 委派尚未启用。

- agent.go：Eino ChatModelAgent 与阶段 Handlers，限制可见工具与累计规划预算。
- state.go：有版本的阶段、模型次数、计划及最后工具引用，随原生断点持久化。
- journal.go：模型决策先持久化，工具绑定可信执行和稳定操作 ID，恢复读取原决策及工具观察。
- adapter.go：平台 Execution 适配、安全停止、上下文构建及真实保存结果渲染。NewExecution 只构造和核验，不调用模型或写业务。
- prompt.go：阶段指令、必要信息追问和能力限制。

业务规则、候选校验与事务在 service/trip 和 repository/postgres；本目录不直接 import PostgreSQL。政策调用已有 Python RAG MCP，未迁入旧 Python Agent 或其数据。查询可显式使用演示供应方，标识随证据、保存候选和输出保留。

验证与缺口见仓库根 docs/travel-refactor-verification.md；正式行为见 openspec/changes/refactor-travel-agent-go。

