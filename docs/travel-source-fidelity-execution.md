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

## Task 2：业务语义与绑定回复

- 新原生业务快照 SourceVersion=1，保留条件/来源/强度、任务修订、查询项目、约束快照和原版停止原因；兼容已有 Go 数据字段。整条路线使用带指针的 patch 输入，省略住宿等字段不会变成清空。
- 恢复 Main 处理新修改与 Verifier 校正权限的区别；Verifier 不能改变已有硬活动、硬条件和路线，返回 REPLAN_REQUIRED；活动时间改动同步已有描述中的旧时间，保留无关车次，修复代码必须引用当前相关问题。
- 完整角色和工具仍未接线，这些业务用例此时通过 Service 的纯 Preview 验证，不代表已经经过真实 PostgreSQL 或真实模型执行。
- 原版高铁分类按完整车次规范为 high_speed/emu/other/unknown；Service 可识别旧 Go 的 G/D 表示。金额经精确有理数转换整数分，人数乘价和每晚预算不使用浮点或整数截断。
- 恢复酒店地点价格未知的有条件阻断、粗时段重叠不确定性、真实日期/跨段时间比较、当前候选证明的硬日期冲突；无候选或存在可行候选时不误认全部不可能。已证实的硬日期冲突不启动无用查询。
- 绑定回复验证本次实际回执的行程/修订及 select/amend，支持 content 局部回复和路线整体展示；没有回执、旧修订、proposal 回复、错误组件引用均拒绝；不把模型解释作为事实。
- RED→GREEN 覆盖：规范车种、14:00→16:00、局部组件范围、整路线省略值、仅查询新 items、硬预算继承、报价需求证据、粗时段、硬日期冲突、人数金额、每晚不足一分的边界、具体错误、回执与回复范围。按综合用例组织，不移入 Python 测试集。
- 验证：`go test ./internal/... -count=1` 退出 0；受影响 Trip/Train/Tools/Travel 四个 package 重跑通过；对应 `go vet` 退出 0；`git diff --check` 退出 0。原始日志 `.cache/execution/travel-source-flow/task2-*.log`。

Task 1/2 的阶段检查完成。工具实际接线、完整 Eino 角色编排、政策/偏好/压缩辅助模型、恢复和真实模型验收仍未完成，不得据此声称整次重构完成或原版效果已经恢复。
