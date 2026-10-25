## Context

当前根分支feat/agent-platform-mvp，HEAD8e9094c。审计发现旧数量验收掩盖business→visit，7/8次多城路径中同一selection_refs提交3次。错误已通过nativeBatchGate→recordRejected→LastBatchResults→previous_round_summary.results返回，但仅有枚举和通用字段说明；测试本身还明确要求“演示/无需政策”，与正常出差工具前置步骤相冲突。

授权依据：用户2026-10-25要求“针对测试和提示词进行修改”“校验反馈也改，你之前总结的问题就改”，并明确最终输出也将夹具当普通数据。原任务已选单代理，继续沿用；本次范围不另行引入产品选择。

## Goals / Non-Goals

目标是修复已查明的问题并提供真实纠错/计量证据。保留模块边界、原版角色和原始golden，新增纠错补充可追溯。不改线上供应方能力、不新增预订交易，不移除业务保护，不调大轮数/时间/重试，不复制上千测试，不引入新Agent框架或依赖。

## Decisions

### D1 测试供应方与展示demo模式分开

新增backend/tests/integration/travel_evaluation_fixtures_test.go，代替性能测试直接复用demo.New。完整夹具返回正常车次/酒店报价/库存/天气和可验证政策引用，名称、证据ID、来源、用户query及最终答复无fixture提示；IsDemo不启用，报告单独标tool_mode=fixture。酒店仅地点负例独立保留无报价，不通过补价遮盖能力缺口。已有线上demo模式标记和真实高德地点边界不变。

### D2 保留原文、补充定向纠错

新增Travel prompts/correction.txt并embed。实际beforeModel输入中增加单独system提示：business保持用途，政策未观察时先query_policy；arrival_before属于conditions而非constraints；错误只修反馈指出的内容；未知价格/政策不因重复select变为已知，按原部分收尾规则处理。原文件及旧hash不改；新补充内容计入workflow fingerprint，升级flow/batch版本，拒绝不兼容旧断点。测试分别断言原文基线和当前实际补充，报告披露提示版本。

### D3 反馈是可恢复的工具观察

Registry仍先规范化并预检完整批次，business在pre_policy时反馈POLICY_OBSERVATION_REQUIRED、缺少的步骤和“保持business，不换成visit”。对已证实的arrival_before错位提供精确到任务下标的field/correct_path，不自动搬字段或更改用户值。预检错误附根因call_id；nativeBatchGate向根因返回实际错误，对其他项返回BATCH_ABORTED并引用根因，明确executed=false；身份、授权、版本、硬约束校验不变。

不依赖异常文本echo用户值来生成指引；只使用已知工具字段路径、固定错误码和允许结构。通过原journal持久化及下一轮review回传，不加第二个自定义重试循环。真实Eino脚本测试在下一次Generate内读取反馈，证明并非只在日志里出现。

### D4 修复源能力缺口分类，而非任意提前终止

service/trip finishSourcePlan的gapOnly与Python _capability_gap_only对齐：允许hotel_price_unknown/total_budget_unknown及evidence命中hotel_*政策rule.id的policy_constraint_unverified；必须有已选且无报价的酒店地点。无关政策未核验、缺车次和硬冲突不归入此类。沿现有StopPlanning→safe point确定性收尾，不额外调用模型，不修改原无进展阈值。

### D5 强化验收，少量代表性测试

沿用真实PostgreSQL随机schema。原12/12报告不覆写，新增一次四场景真实模型测量，必要时只复跑定位后的受影响场景，禁止循环挑最好结果。新报告分别给semantic_success、efficiency_success、rejected_batches/root_calls/collateral_calls、duplicate_selections、stage calls/Usage/latency。完整证据的目标路径：查询2次、出差至少Main政策决策+政策提取+Main创建+Verifier选择4次；这是解释基准，非调低程序硬上限或保证任意模型恰好4次。

捕获实际送给模型的脱敏messages和工具定义供本地审计，报告不要包含密钥；不把fixture元数据注入messages。使用动态未来日期一次捕获并写入报告，避免固定日期过期。修改会议用例断言原车次ref完全相同，而不仅仍有一个Train。用途、路线、日期、活动与无重复均独立验收，不将“运行结束”解释为目标满足。

## Risks / Trade-offs

- 提示可降低模型错误，不能保证模型永远遵守；程序保护与明确失败指标保留。
- 完整报价夹具仅衡量正常执行路径，不代表现有高德客户端提供报价；缺字段负例独立验证。
- 原版基线保留，实际提示新增补充是本次用户明确授权的变化，不能继续宣称所有有效提示完全原封未动。
- 不将已知政策缺口一概当供应方能力不足；限定rule.id与酒店报价缺失的事实关联。

## Verification

三个实施任务各用有意义的综合RED→GREEN。重点是完整模型输入/输出无来源提示、根因及连带反馈回传、business不能改成visit过验收、相同选择检测、酒店政策报价缺失的安全收尾。最后跑完整Go/PostgreSQL、vet/build和已有前端检查；按实际情况验证Linux race。OpenSpec无verify命令，使用人工场景矩阵与strict validate。
