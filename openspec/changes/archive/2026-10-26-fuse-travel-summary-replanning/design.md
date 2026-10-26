## Context

用户明确：Main只规划一次，仅差旅政策辅助允许再次Main，正常工具结果全部handoff总结Agent。Python/runtime/state.py的decision_role实际有request_replan→main，故不声称该回边源码不存在；本次是用户授权的定向流程修正。当前registry、review schema与service快照均阻止Verifier改整条路线，这是真正多轮原因，不只是命名。

## Decisions

1. 使用独立可信角色planning_summary，已有只读独立查询summary及旧verifier基线保持。FlowState新增MainUsed、PolicyContinuationUsed；Main响应后工具/预检结果直接进入规划总结，只有真实政策观察且尚未续规划可Main第二次；原request_replan不再返回Main。
2. 新SourceProfile运行分支planning_summary（已有计划仅amend/select）与planning_recovery（首提议失败可propose），从冻结main schema克隆后保留对应分支；不改冻结JSON。总结不开放request_replan和write_memory；read只保留明确历史定位且当前资料未完整的必要场景，独立query只允许既有失败条件重试，新增待查由route_tasks生成。query_policy仅在尚无观察且确需政策时可用。
3. 新角色预检强制AllowedTripIDs；已有计划不许propose，无计划只恢复本请求创建；预检与事务独立核验同用户、修订、attempt和证据，不把角色伪装成main。
4. service允许新角色route_tasks，同时新增最终提交前的硬要求比较：用户/context硬全局及保留任务叶条件、硬预算和硬活动值不能更改/删去；活动按语义跨任务匹配允许修正错误归属但保留日期/时段/强度，任务角色与起终点/用户目的地保持，硬晚数沿用原快照。derived软日历可调整，不新增未授权hard→soft或不同硬事实。
5. Main生成协议/交互失败也handoff规划总结再生成。重新生成必须真正替换角色提示和实际工具集，计量阶段为agent:trip_summary，原2次再生成预算不变。journal接受的是最终真实输入与对应工具hash；恢复先还原角色及Main使用标志，缓存重放不新增生成。
6. 正常为Main1＋规划总结1；中转重排为Main1＋规划总结2；政策为Main2＋政策提取1＋规划总结1。没有把工具返回尚未知的新候选提前编进第二轮，也不要求重排全场景只两次。
7. 单独planning_summary.txt覆盖原Verifier“交Main”语句；同批审查/修改/选择在真正有候选且无依赖时允许，route_tasks需新查询时再总结一次。默认重庆、有效记忆优先、不追问、完整展示、低频维护保持。

验收解释：南京中转的3次目标指无额外模型参数/协议错误的路径；若首次模型日期参数被拒，仍按既有规格允许总结有界纠错，实际次数完整保留。真实模型测试仅凭已记录根拒绝或交互恢复增加对应纠错次数容差；无反馈的额外总结及任何非政策Main重入仍失败，不将用户“大多数、允许特殊情况”收窄为固定3次。

## Risks / Trade-offs

- 总结权限扩大 → 独立schema、显式当前ID范围、硬事实提交前比较、负例先RED；不能直接复用Main全部权限。
- 再生成时工具hash/阶段不一致 → options绑定新工具，journal重新取实际profile并hash；真实输入、拒绝与恢复测试对账。
- 将政策错误当允许Main反复 → MainUsed/PolicyContinuationUsed持久化，只有真实policy观察一次例外；失败及纯query不解锁。
- 旧断点角色/契约不兼容 → 新指纹按现有规则拒绝旧恢复，不假称无缝兼容。

## Migration Plan

按用户已选单代理，沿用codex/travel-normal-flow。输入/角色/schema/硬保护先RED再最小实现，脚本3次路径及失败恢复/政策角色回归；实际模型中转重复三次及常规/记忆/明确条件/政策回归，报告Main与总结实际次数及全部失败。全量Go/PG、vet/build、自审、strict validate及人工规格核对，合格后无活动任务才部署并网页验收，archive同步主规格；分支整合仍待用户选择。
