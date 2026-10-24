# 原版差旅保真迁移实施记录

正式依据：`openspec/changes/preserve-travel-source-flow/`；详细计划：`docs/superpowers/plans/2026-10-24-preserve-travel-source-flow.md`。用户于 2026-10-24 审阅并批准按计划单代理实施。保持当前目录及非主分支原地迁移。

## Task 1：原文与契约基线

- BASE：eb85407。初始受影响 Agent/Tool/Trip 三个 package 单测通过，真实模型旧失败场景没有因此消失。
- RED：TestTravelSourcePrompts 证实当前缩写 Main SHA256 不符，另外四份资源缺失；TestTravelSourceContracts 因没有 source profile 入口失败；NativePatchPresence 因缺少源参数类型失败，追加非法尾随 JSON 的行为断言也先失败。
- GREEN：五份原文按 AST 求值后字节导出并 embed，五个 SHA256 与冻结基线一致；TRIP_ARGUMENT_HELP 原样保留。SourceProfile 以源 ToolDefinition.schema 导出 main/pre_policy/review/review_retry/summary，完整工具说明和 schema 经过 Eino JSON Schema 转换后仍与原值一致。
- 固定了政策/偏好/压缩三个动态提示的固定输入 golden。它们是待 Task 4 消费的基线，不代表辅助模型角色已恢复。schema 导出仅加载原项目类型模块，不启动 Application、不读取其 .env 或数据；禁写 Python bytecode，使用本机 pydantic 2.13.4，未安装依赖。
- Native 参数类型保留 nested tripplan、整数修订和局部 patch 字段存在性，明确区分省略 content 与 content=[]；完整验证和业务映射留 Task 3。
- 添加精确资源的 Git 字节保护，避免 Windows checkout 改换行导致原文摘要失配。
- 验证：`go test ./internal/agent/agents/travel ./internal/agenttools/travel ./internal/service/trip -count=1`，退出 0，三个 package 通过。原始日志在 `.cache/execution/travel-source-flow/task1-*.log`。

当前仅 Task 1 完成。角色编排、工具实际接线、业务完整性、恢复和真实模型验收仍未完成，不得据此声称整次重构完成或原版效果已经恢复。
