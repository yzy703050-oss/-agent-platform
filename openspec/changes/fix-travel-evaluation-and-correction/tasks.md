## 1 测试数据与验收

- [x] 1.1 已替换为独立普通契约夹具，完整报价/库存/政策不标演示，报告独立注明fixture；TestTravelEvaluationContract复现污染RED→GREEN。所有阶段实际输入及最终输出的真实模型复核仍由3.2完成。
- [x] 1.2 已强化用途、路线日期/活动、原候选ref及重复选择判据；原business→visit/三次同选RED→GREEN，实际messages/tools记录器单次Generate及完整schema检查通过。真实模型计量待3.2，不覆盖旧报告。

## 2 定向提示与可执行纠错

- [x] 2.1 原文golden保留，correction-v1补充接入真实beforeModel，加入flow-v5/batch-v5指纹；原提示/契约校验及当前实际输入断言通过。
- [x] 2.2 政策前置与错位路径反馈、FailedCallID和BATCH_ABORTED已完成；TestTravelCorrectionFeedback/ReachesNextModel综合RED→GREEN验证下一次真实Eino Generate收到详情、两个journal结果配对、零写入。完整受影响模块通过。

## 3 部分收尾与验证

- [ ] 3.1 对齐原版酒店政策报价缺失的能力分类；正例无需重复select即可收尾，负例不误停。
- [ ] 3.2 完整Go/PostgreSQL、vet/build及前端回归；一次四场景真实模型验证并记录实际输入/调用/成本，缺项或失败不宣称完成。
- [ ] 3.3 人工规格核对、strict validate、更新文档与任务；只有实际满足才归档并按既有授权保留当前分支。
