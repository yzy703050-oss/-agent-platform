# 差旅 Go 重构实施记录

计划：docs/superpowers/plans/2026-10-23-travel-agent-go.md。正式规格：openspec/changes/refactor-travel-agent-go。2026-10-23 用户批准计划并选择单代理，按精简测试策略实施；本记录不替代 OpenSpec 任务状态。

## 基线

- 现有 Go 全套回归在 REQUIRE_DATABASE_TESTS=1 下通过，包含真实 PostgreSQL 集成；日志 .cache/reports/travel-baseline.log。
- 当前项目内工作，未建立第二个 Go module、工作树或 Python 服务，未读取原 Python .env 或业务数据库。

## Task 1：恢复技术探针

- 将 General 已有 TurnLoop 与 checkpoint envelope 提取到 agent/einoruntime，General 保留兼容入口，停止确认规则不变。
- 旧运行适配测试随实现迁移到新包，原断言保留。General 自己的执行与流式测试继续运行，不重复一套相同测试。
- Travel 使用 Eino ChatModelAgent Handlers，阶段与累计调用计数序列化到 Eino SessionValues，构造内核不调用模型。
- 新增一个聚合恢复测试，包含正常持久化恢复与保存失败子场景，同时检查版本指纹不兼容、已完成写工具不重复和待执行查询继续。
- RED：新恢复包/内核未实现时测试失败；GREEN：`go test ./internal/agent/agents/general ./internal/agent/einoruntime ./internal/agent/agents/travel -count=1` 通过，日志 .cache/reports/travel-task1.log。
- Ruling：Eino 停止后可以保存完整模型响应及尚未执行的工具调用；恢复先执行该工具，再调用模型。测试分别断言待执行工具看到 querying/计数 2、后续模型看到 validating/计数 2。代价：此断言绑定已固定 SDK 的实际恢复语义，升级必须重新验证。
- Ruling：共享运行适配的测试引用私有停止确认字段，应随被测实现迁移，不能用模型 context.Err 替代停止确认；SDK 的安全点取消允许当前模型调用完成。原停止确认断言完整保留。

尚未证明真实业务修订的幂等、完整 Travel Execution 或真实供应方/RAG 接入，这些继续在后续任务实施，不能将此探针描述为差旅功能完成。
