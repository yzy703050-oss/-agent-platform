# 差旅停止优先级与状态转换验收（2026-10-25）

范围仅为用户选定的两项迁移差异，基线aee1bb4，修复分支codex/travel-stop-phase-parity，PR目标feat/agent-platform-mvp。单代理实施和自审；未修改原Python项目、提示词、工具Schema、模型参数或预算，也未运行付费模型评测。

## 1 停止原因

按Python trips/service.py的互斥分支顺序恢复原因选择，校验事实不变。TestSourceStopPriority在旧代码上复现5个失败断言，修复后13个组合均通过，Service全模块通过。

| 组合 | 原Go问题/修复结果 |
| --- | --- |
| 仅政策阻断＋select/confirm＋allow_repair=false | 原先被覆盖为repair_disabled；现保留policy_unverified |
| 酒店能力缺口＋关闭修复 | provider_capability_missing优先，原validation保留 |
| 实际硬日期冲突＋关闭修复 | 保留hard_constraint_conflict，不产生无意义查询 |
| 一般硬校验＋select＋关闭修复 | 按原Python分类repair_disabled；validation仍阻止不合规提交 |
| 一般硬校验＋初次propose | 按原Python为awaiting_model；原校验问题保留以供修正 |
| 输入不足 | 不再用独立if覆盖原停止原因；validation中的输入缺失事实不变 |
| 暂停、取消、有效confirm | 各自早返回，原因和有效提交不被后续分支覆盖 |

不能将“停止原因变为repair_disabled”解释成“硬约束允许放宽”。该字段决定收尾分类，validation及实际提交条件仍独立执行。

## 2 执行阶段

phase.go是Travel执行Phase唯一的活跃赋值入口，按Python runtime/state.py冻结的9阶段转换表检查。TestTravelPhaseTransitions覆盖全部合法/非法阶段对、未知阶段、同阶段幂等、首个停止原因保留及summary/终态禁止新批次；旧实现真实RED，修复后GREEN。

- 正常工具路径：deciding/repair_required → executing → validating → deciding。
- 错误观察：validating → repair_required，下一轮允许合法纠正。TestTravelCorrectionReachesNextModel实际Eino Generate检查阶段，除原结构化错误回传外验证未误回deciding。
- 受限收尾：stopping → reply_ready → stopped；正常答复reply_ready → completed。
- completed/stopped/paused禁止重新进入活跃阶段；非法边返回INVALID_STATE_TRANSITION及from/to，不修改原阶段。
- 快照加载独立验证阶段；未知阶段返回INVALID_EXECUTION_STATE。TestTravelTerminalSnapshot用实际Eino Runner验证暂停/未知快照拒绝、已完成/停止结果重放，均零额外Generate及零业务写入。

Eino WrapModel可能先保存最终答复，之后AfterModel再次观察同一答复。仅当终态、无工具调用且正文与已保存FinalText完全一致时幂等确认，不重新转换阶段。已简化的测试钩子改用生产modelObserved/reserve链，不为测试放宽转换表。

新指纹含travel-source-flow-v6及stop-phase-parity-v1，旧不兼容断点拒绝继续，历史仍可查看；平台Run/attempt和业务回执职责不变。

## 3 验证矩阵

| 需求/场景 | 实际证据 | 结果 |
| --- | --- | --- |
| 停止组合与validation不变 | TestSourceStopPriority，13组合，stop-red/green日志 | 通过 |
| 合法/非法边、幂等、终态拦截 | TestTravelPhaseTransitions | 通过 |
| 错误观察到下轮模型 | TestTravelCorrectionReachesNextModel，阶段与错误字段断言 | 通过 |
| 终态/暂停/未知快照边界 | TestTravelTerminalSnapshot，错误码RED→GREEN | 通过 |
| 原角色链、重规划、多计划、证据刷新、无进展与协议重生成 | 既有TestTravelSourceFlowScenarios及协议/批次/能力回归 | 通过，既有场景模型/写入/查询次数断言保持 |
| 全部Go与真实PostgreSQL | REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s | 通过，integration36.716秒 |
| 静态检查和构建 | go vet ./...；go build ./cmd/server | 通过 |
| 最后快照错误码补齐后受影响模块 | Travel/AgentTools/Trip全部模块 | 通过 |
| 最终代码Linux race/PG及前端CI | [CI40157228490](https://github.com/yzy703050-oss/-agent-platform/actions/runs/40157228490)，42eb24ba83c832d58babae542b84d8dda42051c6 | 全部成功，race集成74.487秒，vet/build及前端均成功 |
| 规格核对和归档 | 单代理整体自审、人工逐场景核对、strict validate及archive | 全部通过，主规格同步成功，见[归档任务](../openspec/changes/archive/2026-10-25-fix-travel-stop-phase-parity/tasks.md) |

日志保存在本地.cache/execution/travel-stop-phase-parity。当前OpenSpec CLI无verify命令，已使用上表人工核对，不声称运行OpenSpec verify。CI对应上述精确代码提交，后续归档文档提交不改已测代码。

## 4 边界

本轮未运行Python模型对照，也未重测真实车次/酒店/RAG在线供应方、浏览器UI或负载。这是确定性规则的迁移修复，不声称完整Go/Python行为已全部等价，亦不以此更新上一轮Token/延时数据。
