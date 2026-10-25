## Context

来源只读：Python trips/service.py:768–850、runtime/state.py的TRANSITIONS/transition/stop/observe，runtime/loop.py的persist_phase及recovery.py的最终恢复。Go当前finishSourcePlan按多个if赋值，sourceFlow直接设置字符串Phase。

## Goals / Non-Goals

只恢复两项已确认差异，不改角色、提示、模型参数、预算、工具权限、Repository或页面行为。沿用用户已选单代理，当前仓库干净，使用独立Git分支在原工作目录实施，不增加worktree或依赖安装。

## Decisions

### D1 业务收尾为一次有顺序的原因选择
保留暂停/取消与有效select/amend/confirm的早返回。未完成时参考原源码先计算默认硬冲突/awaiting_model，然后依次处理实际硬日期冲突、仅政策阻断、能力缺口、关闭修复；这些分支互斥。特别注意原版一般硬冲突与allow_repair=false组合的原因可为repair_disabled，但原validation仍阻断提交，不能把原因分类当成放宽硬约束。删除与该互斥选择冲突的额外覆盖，输入缺失的validation事实不变。测试明确覆盖组合而非只测单一条件。

### D2 一个Travel执行阶段转换入口
新增phase.go，将原9阶段表与同阶段幂等封装为FlowState.transition(next string) error、stop(reason string) error及summaryOnly()。未知阶段使用INVALID_EXECUTION_STATE，非法边使用INVALID_STATE_TRANSITION并给出from/to，不修改状态。

转换边：deciding→executing/reply_ready/stopping/paused；executing→validating/stopping/paused；validating→deciding/repair_required/reply_ready/stopping/paused；repair_required→deciding/executing/reply_ready/stopping/paused；stopping→reply_ready/paused；reply_ready→completed/stopped；completed/stopped/paused无外出边。同阶段为幂等。

接入复核：Eino的WrapModel可先保存纯文本最终答复，AfterModel才观察同一答复。仅当completed/stopped且正文与已保存FinalText完全相同、没有工具调用时，该观察幂等返回；不允许终态转回reply_ready。未知快照阶段返回INVALID_EXECUTION_STATE，暂停快照拒绝模型决策；已完成的缓存答复直接重放，均零额外Generate/业务写入。

### D3 现有执行钩子接入
flow.reserve/modelObserved、afterBatch、beforeModel、FlowState.ObserveReview、protocol与finalize均使用统一入口；终态和summaryOnly拦截后续工具。批次结果先进入validating，错误观察转repair_required，beforeModel保留该阶段再进入executing。无进展/预算/协议错误使用stop；有最终答复时先reply_ready再completed/stopped。测试中已有简化钩子也必须走合法边，不为旧测试放宽状态表。

### D4 恢复与指纹
检查点/已接纳决策的快照装载检查已知阶段，重建不是活跃跳转；维持决策重放不重复模型与业务写入。Phase仍为可序列化字段，平台State仅作现有阶段投影。变更工作流指纹版本包含stop/phase规则，不升级数据库，不迁移旧任务；拒绝旧不兼容断点。

## Verification

两项各一个核心综合RED→GREEN：停止优先级组合表；全部阶段合法/非法边、原状态不变和停止原因幂等。已有真实Eino脚本、批次拒绝、协议重生成、部分结果及PostgreSQL恢复集成共同证明接入有效，必要时只补一个边界场景，不扩展大量用例。完整Go/PG、vet/build、Linux race、OpenSpec strict validate及人工场景核对后归档并创建PR。无verify命令则明确人工核对。前端未改，复用CI现有检查；不为该确定性修复增加付费模型调用。
