# 多城市 TripPlan JSONB 存储示例

更新：2026-11-03。本例已按 [组件数据契约 v1](../openspec/changes/refactor-agent-kernel-plan-execute/contracts/README.md) 重写，替代早期 draft-v1 中的 hard/soft 与 select Item 结构；尚未执行数据库迁移。

## 1. 假设用户输入

一位旅客 2026-11-10 北京到上海，上海住两晚；11-12 到杭州，杭州住两晚；11-14 返回北京。个人旅行，总预算 3000 元，酒店每晚 400 元以内，优先靠近地铁，交通优先高铁。

日期、人数、金额和要求均是本例假设用户已提供的信息，不是默认值，也不是供应方查询事实。预算与价格要求交给模型按来源优先级规划和解释，不建立硬条件过滤器。

完整 [JSON fixture](fixtures/tripplan-multicity-snapshot.example.json) 对应 PlanSnapshot，和 [契约示例 26](../openspec/changes/refactor-agent-kernel-plan-execute/contracts/examples/26-multicity-plan.json) 内容相同，由校验器检查一致。

## 2. JSON 内部关系

| 路径 | 表达什么 |
| --- | --- |
| plan_id / travel_thread_id / revision | 计划身份、持续子线程和当前内容版本 |
| definition.stops | 北京出发、上海停留、杭州停留、北京返回；两次北京用不同 ID |
| definition.legs | 三段交通，引用前后 Stop |
| definition.requirements | 用户预算、酒店和高铁要求，保存作用范围与消息来源，无 hard/soft |
| definition.tasks | 三个列车 Task、两个酒店 Task，关联各自 Leg/Stop；详细查询参数在 Task.parameters |
| Task.items | 查询领域及稳定 Item ID；本例参数独立，depends_on 均为空 |
| task_versions | 五个 Task 分别从 task_revision=1 开始，由程序维护 |
| selections | 首版为空，只有真实结果回来后才可保存证据引用 |

没有把每个 Stop/Task 拆成计划表的新行。needs_query 不在不可变 snapshot 内；执行器根据 task_versions 和执行记录生成 PlanView。本例尚未查询，不包含虚构候选、报价、库存或执行成功。

## 3. 两张计划表如何承载

| 目标表 | 本例内容 |
| --- | --- |
| travel_plans | plan_multicity_demo 的身份、用户/主线程归属、TravelThread 绑定、current_revision=1 |
| travel_plan_revisions | revision=1 的完整 PlanSnapshot JSONB；首次无 Patch，后续保存 Patch 及新完整 snapshot |

表名说明职责，非已执行 SQL。新字段和约束以最终 DDL/迁移为准。查询执行、供应方证据、模型提议、可靠操作分别进入另外四张主干表，不能只靠 snapshot 判定业务完成。

## 4. Executor 取什么参数

杭州酒店从 task_hotel_2.parameters.hotel 读取 city、check_in、check_out、guests、keywords，结合计划级预算、该 Task 适用要求和 ranking，生成带 Task/Item 版本及执行代次的 ExecutionRequest。

Stop/Leg 提供路线关系与计划上下文，不再从 Item 找另一套城市和日期。酒店区间在 Task 明确保存，可以只覆盖部分停留；不是永久强制等于 Stop 区间。列车日期是用户计划要求，不冒充已核实到达时间。具体供应商接口的字段转换由适配器负责。

## 5. 修改与重查

如果用户只修改上海酒店位置要求，提交基于 revision=1 的 Patch；受影响 Task 的有效定义变化，task_hotel_1 的全部查询 Item 重查，其他四个定义未变的 Task 保留仍有效结果。不能因为输入哈希碰巧相同就免掉该 Task 重查。

仅选中已查询的真实候选时，计划内容升版而对应 task_revision 不变，不触发查询。用户主动刷新则记录新执行 generation；旧结果迟到不能覆盖新的执行。每次内容变更保存完整新快照，Patch 和回执原子提交。
