# 技术复核记录

日期：2026-10-24。执行方式：单代理；按用户要求整体自审，不派子代理。阶段：方案复核，尚未进入产品代码实施。

## 复核依据

- 本变更 proposal/specs/design/source-baseline/source-mapping。
- 现有 Travel agent.go、state.go、adapter.go、journal.go；agenttools/travel/tools.go。
- service/trip、service/travelpreference、agent/memory、agent/ports 及现有 PostgreSQL 事务与恢复接口。
- Python prompts/loop/state/limits、registry/trip_contracts/normalization、planning/review projection、reply binder、政策/偏好/压缩链路。
- 既有真实模型报告 `.cache/reports/travel-performance-2026-10-24.*`。该报告是修复前失败证据，不能当本变更验收结果。
- 本地已下载 Eino v0.9.0-beta.1 的 adk/chatmodel.go：可改模型前 state.ToolInfos，存在模型后和工具包装钩子。具体整批预检、拒绝观察配对及同轮收尾仍须 Task 3 的执行探针证明。

## 发现与设计处理

| 发现 | 影响 | 处理 |
| --- | --- | --- |
| 只恢复原文但保留当前扁平 schema | 模型按原文发参仍失败 | D3 同步恢复模型契约，不重写提示去适应错误协议 |
| 逐工具才校验，Eino 工具可能并发 | 合法项先写、另一项才报错，不等价于源预检 | D2/D5：整个批次在任何 handler 副作用前预检并持久化结果 |
| 平台 Run 与 Agent phase 混为一体 | 角色切换和停止可能变成错误暂停或假完成 | D4 分开三种状态，恢复校验全部状态 |
| 原预算和 Go 原有平台上限含义不同 | 提高 model iterations 不等于恢复源预算 | D8 分别建模和记录限制原因；不静默修改 General |
| 工具原参数比当前 TripPlan 丰富 | 简单转换会丢硬软、粗时段和整路线修改 | D5 先补业务语义，再接工具 |
| receipt 存在被当作此次请求完成 | 老计划存在但 14:00 没改到 16:00 | D7 绑定本次回执/修订和所需组件，实际保存断言 |
| 政策/偏好/压缩也是模型调用 | 只统计 Main 的性能数字误导 | D6/D9 分阶段统计，不漏子流程调用 |
| 旧指纹可以误恢复新工具协议 | 重放参数不兼容或重复执行 | D4/Migration 更新工作流指纹，旧断点只读展示 |
| 没有 OpenSpec verify 命令 | 无法自动宣称规格一致 | 手工逐项核对需求、场景、任务、测试及证据，单独记录 |

## 依赖顺序

Task 1 冻结提示与契约基线；Task 2 业务语义和实际回复；Task 3 工具协议及整批门禁；Task 4 恢复角色/上下文/政策/记忆闭环；Task 5 批次和状态恢复；Task 6 真实模型度量与平台回归；Task 7 一致性核对和归档。

任务不能以编译通过替代行为验收。Task 4 的完整模型输入必须同时消费 Task 1 原文、Task 2 快照和 Task 3 profile；Task 5 消费 Task 3 稳定动作/待查结果和 Task 4 全量执行状态。实施计划应使用一致签名，避免各层自行维护同一份规则。

## 尚待执行验证

整批预检的 Eino 时序、模型后直接绑定回复的结束边界、恢复时未完成批次续跑，以及真实模型对完整契约的表现，均属于实施任务，当前没有通过证据。本记录不标记它们已完成，也不保证修复后的模型指标或成功率。
