# 差旅运行时提示分层

OpenSpec：`refactor-travel-prompt-layers`。Python原文和冻结工具契约均保留；运行时版本：`prompt-layers-v2`。

## 运行输入

`角色职责 → 公共回复 → 适用规划领域规则 → 适用审查规则 → 必要恢复提示`，之后保留原上下文数据与完整用户/工具消息。工具参数帮助通过实际tools描述传入，不在每个角色重复。

- Main：首次全局规划与唯一政策续规划。
- 规划总结：已有计划时审查、修复、选择与绑定总结。
- 创建恢复：尚无计划时仅恢复propose，不加载已有计划审查，不附reply。
- 旧Verifier：保留原局部权限及交Main边界，不混入规划总结。
- 独立查询总结：只用原summary＋公共回复，不加载规划动作规则。
- 已停止：停止专用答复＋公共回复，不重新规划。

## 原文段落到维护层

表中“合并”表示保留有效语义、移除重复；原文继续保留用于SHA审计。旧未知起点停止、规划总结交回Main两处，按已归档授权规格由现行默认/分工替代；不额外改变行为。

| 原文件及行 | 原段落开头 | 处理及唯一维护归属 |
| --- | --- | --- |
| `main.txt:1` | 你是差旅助手MainAgent，负责全局规划和原生工具决… | 保留映射/合并迁移：角色Main |
| `main.txt:2` | 第一优先级是完整理解原请求及最新修改：覆盖用户明确的每个… | 保留映射/合并迁移：共享规划 |
| `main.txt:3` | 当前默认高铁服务，按高铁估算交通占用，不建议或虚构未支持… | 保留映射/合并迁移：共享规划（高铁及旅游） |
| `main.txt:4` | 统筹整趟时间：区分出发日与活动日；后天上午在上海活动可前… | 保留映射/合并迁移：共享规划（时间/住宿） |
| `main.txt:5` | 先满足真实前置依赖：明确出差或要求政策才获取企业政策，普… | 保留映射/合并迁移：角色Main＋共享规划（政策） |
| `main.txt:6` | 已注入偏好直接使用，缺人数、晚数、席别或返程结合上下文补… | 保留映射/合并迁移：角色Main＋共享规划（记忆/历史） |
| `main.txt:7` | 只依据真实结果决定候选，不生成权威状态、ID、版本或虚构… | 保留映射/合并迁移：角色Main＋共享审查（候选复用） |
| `review.txt:1` | 你是同一Agent的Verifier，负责完整需求验证、… | 保留映射/合并迁移：角色规划总结/旧Verifier |
| `review.txt:2` | 一、需求完整性：对照original_query和最新明… | 保留映射/合并迁移：共享审查（完整性） |
| `review.txt:3` | 二、业务合理性：按完整日期和实际交通时刻检查跨任务衔接、… | 保留映射/合并迁移：共享审查（时刻/证据） |
| `review.txt:4` | 三、有限局部修复：判断根因后做最小且完整的实际修改，复用… | 保留映射/合并迁移：共享审查（最小修复） |
| `review.txt:5` | 局部遗漏用现有修改能力处理；整段路线缺失等超出当前修改权… | 保留映射/合并迁移：角色规划总结/旧Verifier＋共享审查（收尾） |
| `correction.txt:1` | 参数纠错与执行约束（补充版本correction-v1）… | 仅保留历史标识，不运行注入：历史版本标识 |
| `correction.txt:2` | 用户明确要求出差时保持business用途，返程保留re… | 保留映射/合并迁移：共享规划＋query_policy工具描述 |
| `correction.txt:3` | arrival_before的正确位置为tripplan… | 保留映射/合并迁移：write_trip工具说明 |
| `correction.txt:4` | 工具错误是下一步行动依据：POLICY_OBSERVAT… | 保留映射/合并迁移：write_trip工具说明（错误码） |
| `correction.txt:5` | 比较当前选择与本轮已保存结果。相同selection_r… | 保留映射/合并迁移：共享审查＋write_trip工具说明 |
| `no_followup.txt:1` | 差旅交互与执行约束（interaction-v2）：… | 仅保留历史标识，不运行注入：历史版本标识 |
| `no_followup.txt:2` | 直接完成当前请求，不向用户提问、索取补充或要求确认，也不… | 保留映射/合并迁移：共享规划（默认优先级）＋公共回复（不追问） |
| `no_followup.txt:3` | 其他条件缺省也直接决定：未指定日期建议calendar.… | 保留映射/合并迁移：共享规划（授权缺省） |
| `no_followup.txt:4` | 生成前严格按顺序处理：先逐字读取original_que… | 保留映射/合并迁移：共享规划（原请求/记忆/日历） |
| `no_followup.txt:5` | 仅在用户只说“帮我规划明天去上海的旅途”且确无起点记忆时… | 保留映射/合并迁移：共享规划（缺起点、同城） |
| `no_followup.txt:6` | 完整住宿条件必须写在tasks[i].condition… | 保留映射/合并迁移：共享规划（住宿）＋write_trip工具说明 |
| `no_followup.txt:7` | 首次规划就正确编码用户允许调整的条件：用户说“出发日期可… | 保留映射/合并迁移：共享规划（软日期/硬晚数）＋write_trip工具说明 |
| `no_followup.txt:8` | 默认长途往返把完整游玩放在中间日期，返程日只安排实际交通… | 保留映射/合并迁移：共享规划（长途和返程活动） |
| `no_followup.txt:9` | 本轮是新建整趟规划时，Verify即使中途amend活动… | 保留映射/合并迁移：共享审查（完整展示）＋write_trip工具说明 |
| `no_followup.txt:10` | 当前上下文已直接注入全部有效偏好、长期记忆和已有摘要，m… | 保留映射/合并迁移：共享规划（已注入记忆）＋角色Main（历史边界） |
| `no_followup.txt:11` | 规划请求使用request_kind=planning，… | 保留映射/合并迁移：角色Main/规划总结＋query_policy工具描述 |
| `no_followup.txt:12` | 每段的出发日期填写tasks[i].conditions… | 保留映射/合并迁移：write_trip工具说明（逐段日期） |
| `no_followup.txt:13` | content按活动所在目的地归属，可含多天活动；返程前… | 保留映射/合并迁移：共享规划（活动归属）＋write_trip工具说明 |
| `no_followup.txt:14` | 例如A去B首日下午游览、次日上午继续游览后返回A：两项游… | 保留映射/合并迁移：共享审查（实际时刻）＋write_trip工具说明（活动归属） |
| `no_followup.txt:15` | 条件强度按叶字段逐层一致：preference/prop… | 保留映射/合并迁移：write_trip工具说明（叶字段强度） |
| `no_followup.txt:16` | 修改保留的任务时只填写实际受影响的条件与元数据，不重新把… | 保留映射/合并迁移：write_trip工具说明（局部元数据） |
| `no_followup.txt:17` | 规划总结同时完成审查、修复、选择及总结：无冲突时本次se… | 保留映射/合并迁移：角色规划总结＋共享审查（绑定收尾） |
| `no_followup.txt:18` | 参数或硬要求保护错误表示本次未执行，规划总结按字段反馈修… | 保留映射/合并迁移：共享审查（错误反馈） |
| `no_followup.txt:19` | 重排的上下文已包含完整当前计划，直接基于tripplan… | 保留映射/合并迁移：write_trip工具说明（真实ID） |
| `no_followup.txt:20` | route_tasks每项就是一段交通及抵达该城市后的住… | 保留映射/合并迁移：write_trip工具说明（完整路线） |
| `no_followup.txt:21` | 活动id只在原所属task内复用；Main将活动重新归入… | 保留映射/合并迁移：write_trip工具说明（活动ID） |
| `no_followup.txt:22` | 提交前逐项对照原用户query中的每一个明确活动和粗时段… | 保留映射/合并迁移：共享审查（完整需求） |
| `no_followup.txt:23` | 住宿逐晚覆盖抵达到下一段离开的过夜，不能退房后空缺一晚仍… | 保留映射/合并迁移：共享规划（逐晚与硬晚数） |
| `no_followup.txt:24` | 用户只说“住N晚”时硬条件为nights=N，自行计算的… | 保留映射/合并迁移：共享规划（R减N）＋write_trip工具说明（ID归属） |
| `planning_summary.txt:1` | 规划总结阶段（planning-summary-v1），… | 保留映射/合并迁移：角色规划总结（替代旧交Main规则） |
| `planning_summary.txt:2` | 能完成就同次提交select/amend及绑定reply… | 保留映射/合并迁移：角色规划总结＋共享审查＋write_trip工具说明 |
| `planning_summary.txt:3` | route_tasks是当前trip_plan_id完整… | 保留映射/合并迁移：write_trip工具说明（路线/ID） |
| `planning_summary.txt:4` | 例如直达无候选且允许经南京、返程29日硬定、杭州硬一晚、… | 保留映射/合并迁移：共享规划（固定返程硬一晚） |
| `planning_summary.txt:5` | 插入中转的ID按目的地归属：原上海→杭州的task_id… | 保留映射/合并迁移：共享审查（联合选择）＋write_trip工具说明（中转ID） |
| `planning_summary.txt:6` | Main首次工具提议被拒且尚无已保存计划时，由你依据or… | 保留映射/合并迁移：角色创建恢复（失败创建/权限/政策） |

## 冻结证据与运行证据

`TestTravelSourcePrompts`和`TestTravelSourceContracts`继续逐字核对原Python文本/工具schema。运行时测试检查职责隔离、无旧规则覆盖、数据与工具配对保留、重试幂等、实际工具hash和新恢复指纹。真实模型与固定工具夹具的验收记录另存，不代表在线供应方能力。

## 真实模型校正记录

分层初版没有保留足够明确的固定返程/硬晚数、目的地活动归属和中转ID实例，真实模型验收据此暴露问题。对应例子分别归回规划领域、审查和工具说明；没有恢复跨角色通用补丁，也没有修改Python冻结原文。完整逐轮结果见验证记录。

## 当前工具说明

工具说明版本为`tool-instructions-v4`。字段/来源/身份语法由`runtime_write_trip_help.txt`统一维护；创建示例由`runtime_propose_help.txt`维护，选择/修改示例由`runtime_modify_help.txt`维护。新建Main及无计划恢复仅加载创建说明；已有计划修改Main、规划总结及原审查角色仅加载修改说明，实际工具schema与权限仍由原profile决定。

本轮明确给出行程UUID并要求修改时，经原Inspect加载授权计划供首次Main编辑；不以解析结果代替权限校验。实现、全部历史失败与最终通过证据见[验收记录](travel-prompt-layers-verification.md)。
