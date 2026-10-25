## 1 测试数据与验收

- [x] 1.1 已替换为独立普通契约夹具，报价/库存/政策不标演示，报告独立注明fixture；TestTravelEvaluationContract复现污染RED→GREEN。run2所有阶段实际输入、工具schema及最终输出复核通过；酒店报价限定固定城市单晚，不声明多晚/按日库存能力。
- [x] 1.2 已强化用途、路线日期/活动、原候选ref及重复选择判据；原business→visit/三次同选RED→GREEN，实际messages/tools记录器单次Generate及完整schema检查通过。自审补充amend不能掩盖相同选择RED→GREEN，旧报告保留并解释漏判，run2四场景通过。

## 2 定向提示与可执行纠错

- [x] 2.1 原文golden保留，correction-v1补充接入真实beforeModel，加入flow-v5/batch-v5指纹；原提示/契约校验及当前实际输入断言通过。
- [x] 2.2 政策前置与错位路径反馈、FailedCallID和BATCH_ABORTED已完成；TestTravelCorrectionFeedback/ReachesNextModel综合RED→GREEN验证下一次真实Eino Generate收到详情、两个journal结果配对、零写入。完整受影响模块通过。

## 3 部分收尾与验证

- [x] 3.1 TestSourceHotelPolicyCapabilityGap正例复现awaiting_model错误RED→GREEN；限定hotel_*规则ID及无报价已选酒店，负例覆盖无关政策、缺车次、硬冲突及已有报价。真实Eino能力结果后无额外Generate、保存成果仍可见的边界测试通过。
- [x] 3.2 本地完整Go/PostgreSQL、vet/build及前端15项/build通过。真实模型run1为3/4，夹具物理参数错配后修正；run2完整整组4/4、零拒绝/重复，两轮输入/调用/成本均保留。Linux CI40155219953对应ca25daa，race/PG66.121秒、vet/build及前端均成功；首次deadline断言失败及修正记录公开。
- [x] 3.3 单代理整体代码自审、人工逐条规格/场景/任务核对、strict validate通过，验证矩阵见docs/travel-evaluation-correction-verification.md；93份Python源SHA未变，无重要未解决偏差。工具无verify命令，未声称运行；满足归档条件，按既有授权保留当前分支。
