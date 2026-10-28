# Go 原生差旅 Agent

实现平台 contracts.Agent，共享 Run、身份、SSE、页面租约、Eino Runner 和 Repository 断点。General 与 Travel 独立入口，主 Agent 委派尚未启用。

- agent.go：Eino ChatModelAgent 与阶段 Handlers，限制可见工具与累计规划预算。
- state.go：有版本的阶段、模型次数、计划及最后工具引用，随原生断点持久化。
- journal.go：模型决策先持久化，工具绑定可信执行和稳定操作 ID，恢复读取原决策及工具观察。
- adapter.go：平台 Execution 适配、安全停止、上下文构建及真实保存结果渲染。NewExecution 只构造和核验，不调用模型或写业务。
- prompt.go：统一组装角色、公共回复、规划/审查与创建恢复指令；动态上下文保留为数据，重新生成按当前阶段整体替换规则。
- prompts/runtime/：Main、已有计划总结、创建恢复、旧Verifier、规划领域、审查和停止答复；Python冻结原文继续保留用于SHA审计。
- 工具参数说明在agenttools/travel按新规划、已有计划修改和创建恢复分层；工具名称、参数schema和业务权限保持。

业务规则、候选校验与事务在 service/trip 和 repository/postgres；本目录不直接 import PostgreSQL。政策调用已有 Python RAG MCP，未迁入旧 Python Agent 或其数据。查询可显式使用演示供应方，标识随证据、保存候选和输出保留。

验证与缺口见仓库根 docs/travel-refactor-verification.md；正式行为见 openspec/changes/archive/2026-10-23-refactor-travel-agent-go。


当前分层、原段落映射及版本说明见[提示词分层](../../../../../docs/travel-prompt-layers.md)，本次实际测试与逐轮失败见[分层验收](../../../../../docs/travel-prompt-layers-verification.md)。
