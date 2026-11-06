# 数据迁移与切换设计

> 当前边界：平台与Travel共PG实例、不同schema/数据权限，平台Outbox/Kafka/Dispatch经MCP分发。以下为逐类迁移输入，必须复核跨服务所有权/映射；保留业务数据和不兼容断点保护，尚未生成或执行DDL。

本文件属于同一[数据库设计附录v4](README.md)，目标为双入口共享查询及[工程协议](../engineering-protocol.md)中的Job/Outbox/调用账。数量不作约束。状态：可审阅方案，未连接数据库、未生成迁移SQL或执行升级/回退；已有001～005不改，以下阶段不是迁移文件号。

## 1. 现有 18 张表到目标设计

基线来自backend/migrations的CREATE TABLE，不是生产库检查。2026-11-06复核修正：旧18表留在现有schema作legacy历史，新platform/travel/control结构独立串行创建，不默认SET SCHEMA搬旧表。平台既有用户/Session/会话等向platform的必要导入以保留ID、可重放、切换前停止旧执行为前提；最终新写入只在新目标中，不长期双写两个身份权威。新Travel从新流程开始，不给旧平台业务表补字段就宣称完成服务拆分。服务本地约束以[字典第0节](README.md)为准。

下表“保留/扩展”是逻辑兼容映射：原public记录保持，新platform目标按需承接原ID及字段，不表示在旧public表直接ALTER出目标结构。既有001～005保持legacy基线；新增迁移按control→platform→travel的独立版本表/owner串行执行，运行进程只检查版本。冻结旧写入后做幂等导入及guard初始化，再启用新写入；平台legacy读取限定列/权限并沿用归属/来源过滤，Travel不直接读public历史表。

| 现有表 | 新流程 | 历史处理 / 迁移要点 |
| --- | --- | --- |
| users | 原表保留 | 身份与原 ID 不变 |
| sessions | 原表保留 | 登录租约不转作后台任务授权 |
| user_settings | 原表保留 | 不混入长期记忆版本 |
| page_leases | 按接入授权/页面存在性用途保留复核 | 新流程不再由页面离线触发用户暂停或要求手动继续 |
| conversations | 原表保留 | 每个会话建立相同ID的entry根Thread，agent_kind匹配general/travel；不批量改入口类型 |
| agent_runs | 扩展 | 老行 kernel_version=legacy；旧 terminal/attempt/idempotency 不伪造为新执行 |
| messages | 扩展 | 老消息归根 Thread，保留原 ordinal/source_key；无法证明类别的标 legacy，不假称原始用户输入 |
| run_events | 原表保留 | 旧 payload 原样；新事件增加可选内部路径 |
| checkpoints | 保留旧记录并演进Job版本结构 | 可用新表/过渡列区分legacy与job记录，旧字节不解释为新内核可恢复，不通过强改主键删除旧行 |
| travel_plans | 原schema历史保留；新Travel schema另建 | 旧快照标legacy，仅保留只读路由，不为旧计划伪造可执行TravelThread或跨schema FK |
| travel_plan_revisions | 原schema历史保留；新Travel schema另建 | 旧snapshot不改写，新格式仅由Travel本地修订保存 |
| travel_query_evidence | 保留历史，新流程停写 | 新查询事实写tool_evidence；旧证据保留原ID/内容和legacy读取，不给旧行虚构tool_calls、Task或Item |
| travel_decisions | 保留历史，新流程停写 | 原snapshot/提议和既有引用仅作legacy读取；新操作依据写actions，不为新流程建立到该表的外键，不物理删除 |
| travel_actions | 原schema保留；新Travel schema独立actions | 旧action/receipt在legacy读取适配中按联合身份和摘要核对；不默认复制成新Travel已执行操作，不因提议存在改成succeeded |
| travel_operation_receipts | 保留历史，新流程停写 | 收敛到 actions.result；冲突进入人工可审查清单，不能覆盖一方 |
| travel_preference_revisions | 保留历史，新流程停写 | 迁入用户级记忆需要明确键值/来源/撤回映射，不能把所有旧推断当明确用户意愿 |
| travel_memory_summaries | 保留历史，新流程停写 | 保留其旧长期摘要含义；不能直接变成 Thread 短期摘要或十轮覆盖游标 |
| travel_query_jobs | 保留历史，新流程停写 | 旧作业无新 Task/generation 身份时不伪造 execution_steps；新请求只调度新表 |

platform新建入口threads/delegations/dispatches、入口tool_calls/evidence和用户记忆；travel新建tasks/threads/messages/checkpoints、专业tool_calls/evidence及计划业务。从新流程开始可靠写入，或仅接收经验证的显式导入。不为旧Run编造工具调用、可执行专业Task或嵌套checkpoint，不建立跨服务业务FK。

新增agent_jobs、outbox_messages、outbound_calls、resource_limits及Job checkpoint格式；origin_session_id无法证明的旧Run只保留legacy，不伪造前台授权。新增运行表从新请求写入，不把旧travel_query_jobs直接转换为可执行新Job。Kafka消费者上线前先具备事务/围栏及Outbox，不能先移除单实例锁再补协议。Redis缓存可冷启动，预算/调用未知状态不能从空Redis推导为零消耗。

## 2. 逐类兼容矩阵

以下是本轮推荐的兼容处理，随 Design 交用户审阅；不宣称已实现或已验证。若需要把旧计划直接改造成新计划，应先证明转换并补齐对应规格/测试，不能让实施计划暗改此矩阵。

| 数据 / 操作 | 读取/展示 | 新内核执行 | 核对证据 |
| --- | --- | --- | --- |
| 旧用户/助手聊天 | 保留；按原权限读取 | 可经有效历史适配作为上下文，旧工具协议不作为可执行请求重放 | 来源、消息顺序、工具配对及 UI 过滤回归 |
| 旧已完成计划 | 历史只读适配 | 未证明转换前不直接提交新格式 Patch；General 可按当前新要求创建新计划/线程 | 老 fixture 展示不丢字段，新最小计划不夹带旧内部状态 |
| 旧证据/回执 | 原版本保留可追溯 | 仅在明确适配证明归属/时效/输入覆盖时使用；不能冒充新 Task 已查询 | 价格/库存未知、无 plan 证据、旧 ID 映射测试 |
| 旧明确/推断偏好 | 有效性与撤回按旧数据可追溯 | 经显式映射导入或兼容投影；保留 authority、来源和 tombstone，不无条件全部激活 | 同用户、原键值、原修订/来源、撤回与失效场景 |
| 旧长期摘要 | 保留原历史 | 不当作新短期摘要或“已总结 N 轮”的证明 | source_runs 原样；十轮状态新建 |
| 旧暂停 checkpoint | 字节和原因保留，不再提供旧继续按钮 | 不直接迁成可续跑的新Job；切换时核对旧执行已失权，记录interrupted/兼容原因并解除非终态占用；不改写历史 | 原事件/attempt和来源排除保留，终态后才能接新Run；新故障恢复仅使用已验证新协议 |
| 切换时旧 running Run | 保留事件和部分输出 | 先停止旧进程/证明失去旧执行权，再恢复协调；不能新旧代码同时执行 | 租约/attempt 围栏和查询已提交边界 |
| 新格式计划/记忆 | 新 Schema 读取 | 只交新内核及已验证兼容读法 | 结构版本与路由覆盖 |

旧历史的保留不意味着应把全部旧内部工具日志塞入新提示词。可读、可作为有效上下文、可恢复执行是三个不同判断。

## 3. 分阶段演进

### A. 盘点与只增结构

1. 实施阶段先在隔离测试库确认实际迁移版本、约束、旧数据格式分布和活动 Run；本轮不读取真实用户内容。
2. 后续新迁移先新增表和可空过渡列，新写入路由仍关闭。旧列、索引和历史表保留，旧程序仍可运行其原流程。
3. 用代码适配器/格式标识区分 legacy 与 v1，不把所有旧 JSON 强改为 schema_version=1。

### B. 可重放 backfill 与约束

1. 为每个旧会话按原入口建立根Thread，老 messages 回填 root thread 和保守 message_kind，保持 ID/ordinal/原文不变。当前短期摘要和十轮计数/两个游标直接放 Thread，不建立独立摘要/周期/检查表；source_key 唯一键变更需协调新旧写入。
2. 保留原schema旧计划/修订/证据及legacy显示适配；不为满足新计划约束创建虚假的Travel执行线程。若未来显式导入，另验格式、来源及不可执行状态。
3. legacy读取适配逐条核对action/receipt的`(user_id,run_id,operation_id,kind,request_hash)`，仅展示可证明的原成功结果；冲突保留双方。不把旧receipt回填成新Travel业务提交，旧preference action不转成新差旅操作。
4. 记忆导入锁已有 users 行，核对最新用户快照版本，保留稳定 memory_id 和内嵌单项版本后写完整 user_memory_revisions.snapshot；使用确定性导入 operation_id 及原始修订映射防重复；memory_operations.kind=legacy_import 仅允许迁移器使用。若键值不能忠实表达为新文本/来源，不导入为活跃记忆，继续保留旧读取材料。明确/推断、撤回链和原来源全部保留，不能只挑看起来有效的旧值导致复活。
5. Thread 中的十轮周期从切换后的有效新轮次起建立，不从旧 TripPlan 数、旧 covered_trip_ids、消息数或内部工具数换算。旧历史仍可作为允许的上下文，但不伪造已完成检查/总结游标。
6. 校验回填覆盖和复合归属后再加强新格式约束；旧格式仍允许尚无新执行身份的字段为空。没有证据时不把 nullable 列一刀切为全表 NOT NULL。

backfill 只做可证明映射，不能连接模型批量改写原始快照。导入清单/冲突报告是实施证据，不保存成另一套新的业务权威表；新 memory_operations 和目标唯一键承载重复导入保护。

### C. 切换写入与恢复

1. 暂停接收新执行，处理旧活动/暂停 Run；不绕过 one_nonterminal_run 索引创建第二个运行。
2. 旧执行器停写后启用新内核，更新指纹、格式和工具装配；会话入口保持general/travel；同一个请求的Run幂等键不因新控制流而被当成另一份提交。
3. 新请求按新格式写入；两个入口查询写tool_calls/tool_evidence，规划额外写execution_steps，计划业务成功写actions.result，记忆写用户级表。六张旧决策/证据/作业/回执/记忆表停止新流程写入，保留历史查询适配。
4. 新格式actions不依赖旧decision外键，必要依据和参数固化；prior_replans从操作、修订、执行报告和必要事件装配，拒绝提议不能被迁成成功操作。
5. 使用已批准场景证明：首次/继续子线程及委派上下文追加幂等、模型提议与提交分离、查询代次、部分结果、Maintenance记忆及来源撤回；这里追加不是活动Run补充输入。

6. 对新运行结构验证[本机故障场景](../local-validation-scenario.md)及[交付故障矩阵](../../../../docs/architecture-final/04-validation-and-delivery.md)；切换时先停止旧执行及恢复循环、证明失权，再启用新消费者/Publisher。回退先停止新接纳与发布/接管，保留未完成Outbox和Job，不让旧二进制消费新Topic。旧T0～T6/V1～V12章节编号已撤换，不再引用不存在的章节。

### D. 验收后保留与清理

历史表不在本变更默认删除。旧流程代码和兼容适配的清理应有读取范围、验收证据和后续任务，不能为满足目标表数量而清空历史。

## 4. 回退边界

- 新写入尚未启用：停止新代码，保留增量表/列，旧入口按原能力恢复；必须验证过渡索引和默认值仍兼容旧代码。
- 已产生 v1 数据：首先停止新写入/后台维护，保留全部记录。旧程序不能直接读取新 NULL expires_at、新 Schema 或新记忆语义；优先采用兼容版本继续只读或关闭新执行入口，不能直接宣称切回旧二进制即可恢复全部能力。
- 不执行删除新计划、丢弃记忆修订、重置版本指针等有损 down migration 来制造兼容。若要求降级后继续执行新数据，需要单独证明兼容写法并审阅方案。
- 失败后台任务停止接管时保留 operation 和租约代次，恢复执行先更换 lease_epoch；不能让旧工作者在回退后继续提交。

## 5. 实施阶段必须具备的证据

| 场景 | 通过条件 |
| --- | --- |
| 全新数据库迁移 | 原迁移及新增迁移顺序可执行，所有复合 FK/唯一键正确 |
| 含旧数据升级 | 原 ID、原文、修订、回执和证据保留；新旧格式路由明确 |
| 重复 backfill | 不新增重复 Thread/记忆修订/成功回执，不更改既有原文 |
| 部分回填失败 | 可从检查点/确定性 ID 重跑；冲突可审查而非被覆盖 |
| 旧暂停 Run 切换 | 不续跑不兼容 checkpoint，不永久占用主会话非终态名额 |
| 父恢复等待子调用 | 同 delegation/input 只追加一次，成功 action 不重复提交 |
| Job/消息/调用账迁移 | 原身份保留，新Job断点与旧记录分离；重复backfill不凭空发命令或退款未知消耗 |
| Kafka/Redis部署切换 | 原执行者围栏有效，待发消息不丢；新主丢缓存/许可不突破硬预算或重启已完成业务 |
| 两个入口直接查询 | 不产生TripPlan/计划线程/规划委派；产生真实调用和证据 |
| 差旅入口直接规划 | 复用本入口Run，不调用General或另建UI会话 |
| 查询转规划 | 权限/条件/模式/时效符合才引用原证据，正式选择可靠提交 |
| 工具成功后计划接受前崩溃 | 从原tool_calls/证据恢复接受；Task失效则保留事实不推进任务 |
| 决策表精简 | 旧决策历史可读、新流程停写；actions参数/依据固化，prior_replans无需旧表，记忆decision_key不受影响 |
| Task 变化与选择 | 全 Task Item 重查，无关 Task 复用；只选择不重查 |
| 刷新/迟到结果 | generation/lease 拒绝旧结果作为当前完成 |
| 跨用户和跨主线程 | 复合归属生效；默认目录只取当前主线程 |
| 记忆并发及明确撤回 | 全局 CAS、同项版本和来源检查防止丢更新/复活 |
| 空记忆更新 | 只推进成功总结覆盖，不新增内容修订 |
| 清除与后台交错 | 原 epoch 的任务不能把已清除来源重新写成有效记忆 |
| 升级后回退 | 新数据不丢；无法兼容执行的范围如实保留并停止写入 |

以上是未来测试要求，没有在本轮执行。数据库设计材料的实际静态检查另见 verification.md；它不替代这些数据库/集成验证。
