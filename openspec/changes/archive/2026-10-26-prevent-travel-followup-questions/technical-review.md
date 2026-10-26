# 技术复核

2026-10-25；本次仅阅读与规划，没有产品代码改动。基线经 `git fetch origin main` 核对，本地 main 与 origin/main 均为 `2a4b3c6`，左右差异 `0 0`。实施需从最新 main 建独立 `codex/` 分支，保留本 change 文档。

## 已确认的用户要求

- 不主动追问或要求确认，缺省使用原有软建议，未知事实用陈述句说明。
- 长期记忆直接注入，不由 Main 主动读用户资料或历史取得偏好。
- 普通无冲突规划通常 Main/Verify 两次；这不是所有请求的硬上限。
- 出差先发政策检索工具，取得 RAG/政策观察，再交 Main 生成依赖它的 tripplan。政策前置单列。
- 总结发现规划不合理时允许局部修复、重排与受影响工具调用，具体参照 Python。

## Python 来源与 Go 对照

| 事项 | 只读核对的 Python 来源 | Go 现状与结论 |
| --- | --- | --- |
| 直接注入 | `bootstrap.py:70` refresh_memory_context；`runtime/loop.py:328` 注入 system_context | `flow.go` 已注入 legacy_preferences/memories/preference_summary。应验证真实两阶段输入与空记忆行为，不增加模型读记忆工具 |
| 低频归纳 | `bootstrap.py:108` 自动检查；`agents/preference.py:56` 每三条新增有效确认行程 | `PreferenceAgent.Maintain` 与阈值设计保留；无新增样本不应调用模型，调用维护单列。此前“删除所有自动归纳”的初步方向已撤回，避免偏离原设计 |
| Main 规划 | `agents/prompts.py` MAIN_PROMPT；`runtime/loop.py:270` 角色选择 | 首次已应形成完整 tripplan 与查询地图；新规划先 read 历史并 request_kind=lookup 是模型行为退化，不是原版授权去补读记忆 |
| 自动工具 | `runtime/execution.py` 批次预检与 automatic_queries；`runtime/trip_jobs.py:59` enqueue_trip_query | `PlanQueryJobs`/`runJobs` 已对应；不应为展开查询再次调用 Main。查询缓存/singleflight 已存在，应先测再决定是否需要修改 |
| 政策依赖 | `tools/registry.py:120` creation_registry；`runtime/execution.py` POLICY_OBSERVATION_REQUIRED；`agents/policy.py` RAG→一次提取 | Go 有同等前置保护，保留且测试实际 Main 输入看到政策结果；不能为压次数取消政策依赖 |
| 选择与回复 | `runtime/state.py:78` ready_reply；`runtime/loop.py:444` staged 直接收尾 | Go `ReadyReply`/`afterBatch` 现有路径可两次完成；模型缺 reply 时仍可能再调用，需测试并仅在实际完成、回执完整时确定性呈现 |
| 局部与整体修复 | `tools/registry.py:158` trip_review_registry；`:190` request_replan；`runtime/state.py:27` decision_role | 保留 amend 与单独 request_replan；不重复新建、不抛弃有效成果或重置预算 |
| 无进展 | `runtime/state.py:176` observe_review 忽略回执、解释和修订噪声 | Go 已有指纹保护；不将其改成两次硬限制，正常收尾应早于进入重复循环 |

## 模块、失败与兼容性审查

- 提示补充独立保存并计入 fingerprint，冻结提示和工具契约不改；不会另起一套需求规格。
- 保留程序读取已授权有效偏好，这是注入必需的数据操作；禁止的是模型为填问卷主动查询资料，以及为获取已有记忆额外做分析。
- 不增加意图分类模型。通过现有角色指令及调用审计约束 Main 的新规划行为，实际模型测试失败时不宣布修复。
- 收尾只能由实际匹配 call_id、计划修订及成功回执驱动；存在未配对调用、写入失败、缺候选、硬冲突或不满足目标时不得用事实渲染绕过。
- 同查询缓存命中不等于没有多余模型调用；供应方调用、模型工具尝试和模型调用分开计数。恢复重放与新决策无进展重复也分开。
- 保留原版政策、低频归纳、预算压缩与重排；全部额外模型请求计入总账并按原因分组。正常两次是效率目标，不是强行停止条件。
- 修改提示/收尾语义必须更新指纹；旧断点不兼容时按既有机制拒绝恢复，保留历史。

## 已执行的核对

- `go test ./internal/agent/agents/travel -run 'TestTravelSourceFlowScenarios|TestTravelSourceFlowState|TestTravelSourcePrompts' -count=1`：PASS。
- 当前真实失败样本：`c3b5c321-cef1-4a6b-953f-aeb620023dc3`，Main 主动 read 两项历史，Summary 追问。两次调用但未完成规划，不能将次数当成通过。
- 原版源码仅只读读取，没有启动 Python 模型 A/B，没有更改源项目或旧用户数据。

结论：用户澄清后无未决产品边界；方案可展开实施计划。产品实施仍待用户审阅计划并选择执行方式。
