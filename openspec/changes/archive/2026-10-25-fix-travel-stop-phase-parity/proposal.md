## Why

对照Python源码发现两项仍存在的迁移差异：Go收尾分支会覆盖已经选定的停止原因，且缺少原版集中执行的合法状态转换检查。用户已选择先修复这两项，再向现有默认分支提交PR。

## What Changes

- 按Python trips/service.py的收尾分支顺序选择停止原因，保留业务校验和硬约束阻断，避免policy_unverified被allow_repair=false覆盖。
- 恢复runtime/state.py的9阶段转换规则，同阶段幂等，非法转换返回结构化错误；将执行、批次反馈、协议错误、无进展和最终输出接入同一规则。
- 恢复快照与活跃转换分开，保留平台Run/attempt/回执隔离；更新工作流指纹，拒绝旧不兼容断点。
- 只补关键综合回归，完整Go/PostgreSQL及Linux race验证后创建PR，不运行Python模型对照、不追加付费模型评测。

## Capabilities

### New Capabilities
无新增顶层能力。

### Modified Capabilities
- `travel-source-fidelity`：明确收尾原因组合优先级及原版合法执行状态转换。

## Impact

修改service/trip与agent/agents/travel执行状态模块及少量测试，无新增依赖、迁移、提示词或工具Schema变更。分支codex/travel-stop-phase-parity，PR目标feat/agent-platform-mvp，基线aee1bb43df5265dcd5cd6db14457151cd82f021b。
