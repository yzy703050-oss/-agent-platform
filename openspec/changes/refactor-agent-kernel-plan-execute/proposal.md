## Why

保留General和差旅助手双入口，共享查询能力，将正式规划拆为独立Travel专业服务；在现有工程上重构内核，保留身份、来源、事务、回执和恢复保护。当前采用平台Outbox/Kafka分发，经Dispatch Worker通过MCP提交独立Travel。

用户已确认Run默认300秒、同PG实例按schema/权限隔离、子Task最终failed交回父而不自动新建、PG not_before持久延迟及工具本地timer退避。完整阅读见[整体架构](../../../docs/architecture-final/README.md)，正式技术依据见[Design](design.md)。本轮只整理文档，未实施产品。

2026-11-06技术复核期间补充确认：Travel默认最多8个“执行＋重新规划”轮次，连续2轮无实质进展结束并返回真实部分结果，恢复不清零；仍受总期限和累计预算约束。

## What Changes

- 保留 General 与专门差旅助手两个用户入口，共用身份、会话、Run、记忆和查询底座；入口类型不是部署进程。
- **BREAKING** Travel由平台内置执行改为独立MCP服务，平台通过MQ将委派交给Dispatch Worker，再经受控MCP适配提交/查询/取结果；子Agent无需消费平台Kafka协议。首版实际接通Travel，不增加动态Agent发布平台，见Design及[当前工程协议](mq-mcp-dispatch-protocol.md)。
- **BREAKING** 用户按普通提问与回答交互；不提供活动 Run 补充消息、用户暂停、手动继续或用户主动取消整轮任务的按钮、接口及模型工具，撤回此前步骤边界输入注入方案。同会话执行互斥继续沿用，不新增排队或并行问答。
- **BREAKING** 页面离线不再触发用户暂停；内部专业委派通过可靠等待释放父执行槽，合法子结果由系统续跑；RAG同步检索保持调用方running、续租并占原槽。宕机恢复优先复用已保存输出、查询结果和业务操作记录，以减少模型调用；不承诺零重算或不兼容旧断点可恢复。
- 两个入口都可直接查询交通、酒店、天气、网页和知识库，纯查询不创建 TripPlan 或计划工作线程；实际可用能力以供应方为准。
- **BREAKING** General 识别到行程规划或修改时委派差旅规划能力；差旅助手入口直接进入同一规划子流程，无需先经过 General，内部调用也不创建第二个用户 Run。
- 使用 Eino ADK AgentAsTool 和 Plan-Execute 编排；Executor/Replanner 由 LoopAgent 循环，业务执行按正式计划驱动。
- **BREAKING** 替换本 change 早先“所有差旅查询建立最小计划”的决定：仅规划、计划修改或正式保存选择进入 TripPlan；正式计划内的查询仍由 Executor 根据已提交 Task 驱动。
- 查询调用与证据从计划业务中分层：建议新增 tool_calls、tool_evidence，证据无须 plan_id；Travel 执行项与选择引用共享证据，不复制另一份事实。
- 首次创建覆盖当前请求的完整或最小计划，后续提交按稳定 ID 的 Patch，保存完整快照与修改记录；Task 保存查询定义，Item 仅表示查询及 needs_query 状态，被修改 Task 重新查询，其他 Task 的有效结果保留。
- **BREAKING** 要求按政策高于用户、用户高于记忆供模型规划与排序，不再按 hard/soft 标签筛选候选；继续保留结构、权限、版本、引用与真实结果校验。
- 演进执行项和证据存储；保留事务、权限、回执、恢复及查询客户端能力，替换旧 Main/规划总结的控制流程。
- 用户最新确认省略独立travel_decisions。travel_actions保存受理操作的结构化参数、必要依据和可靠回执，revisions保存实际变更，run_events保存必要拒绝/错误；旧决策表保留历史，新流程停写。
- 数据按平台与Travel划分所有权，各自保存任务、执行现场、调用账和结果；平台保存Delegation、事务Outbox、持久分发操作及远端任务映射。首版共用PG实例、不同schema及数据权限，无跨服务业务联合事务，不固定表数。Stop/Leg/Task/Item与业务回执设计保留，物理映射需复核。
- **BREAKING** 长期记忆按每个入口主 Thread 的 10 轮新增有效对话，由 Maintenance 进行程序检查及模型必要性判断，有必要才后台总结；允许空更新，检查/总结覆盖分开。明确记忆工具仍由入口模型调用，不等阈值、不做关键词检测。
- **BREAKING** 子Agent调用分发采用PG事务Outbox与Kafka，Dispatch Worker持久认领后经MCP提交，Travel本地任务通过自身执行池/租约运行，平台可靠收集后续跑。不恢复旧entry/travel双Topic或同库父子Job事务，也不重复直接向Travel发Kafka执行消息。Travel→RAG仍同步MCP工具，无RAG任务/结果MQ。
- 执行发布/接纳、子任务新调用/提交及结果回填均校验当前状态、deadline、授权和代次。全局deadline到期终结且不重试；用户后续将局部可重试失败改为最多三次重试、四次总尝试，累计次数不因重投/恢复清零。Outbox首版只后台轮询、不实现主动唤醒，PG not_before到期投递，不默认增加Kafka延迟Topic或Redis ZSET调度。
- 沿用 Cookie/PG Session/CSRF、SSE补读、providerhttp及Redis缓存/共享准入方向；适配多实例、跨服务授权、预算和结果归属。执行可靠性见mq-mcp-dispatch-protocol，公共设施见engineering-protocol。
- 旧入口、旧计划和旧断点的迁移策略在实施前确认；不清空历史、不预先承诺无损兼容。

## Capabilities

### New Capabilities

- `agent-delegation`：双入口、独立MCP子Agent持久受理/状态/结果、首次必要上下文快照、持续TravelThread、主线程增量及结果表达。
- `tripplan-lifecycle`：规划建立计划、纯查询免计划、完整创建与 Patch、条件快照、版本化执行和证据复用。

### Modified Capabilities

- `agent-execution`：逐轮问答、单会话执行互斥、无用户补充/暂停/继续/整轮取消，保留系统期限与失败收尾。
- `task-recovery`：移除页面控制与手动恢复；保留内部异步等待、可靠断点、合法系统续跑及结果复用。
- `context-isolation`：移除新问题替代暂停任务和继续入口；保留历史、清除边界及旧失效来源隔离。
- `streaming-and-delivery`：连接与执行解耦，页面重连查询/补读，恢复演示改为内部异步与进程故障场景。
- `identity-and-conversations`：多实例 Session/CSRF、执行授权、数据隔离及移除手动暂停/恢复后的会话管理措辞。
- `travel-execution`：双入口共享查询与同一专业规划执行，旧角色流程改为计划执行和重新规划。
- `travel-planning`：面向任务的计划及请求范围，移除对旧默认流程和模型工具契约的硬绑定。
- `travel-source-fidelity`：历史基线保留用于追溯，新运行时按当前架构定义角色与协议。
- `travel-evidence`：纯查询和计划查询共用真实证据，保持来源和能力边界。
- `travel-memory`：每10轮由 Maintenance 检查并按需后台归纳，允许无新增记忆，保留有效来源、撤回和增量覆盖。

## Impact

涉及 `agent/agents/general`、`agent/agents/travel`、`agenttools/travel`、`service/trip`、领域模型、PostgreSQL Repository、后续迁移、bootstrap 及相关测试。外层 contracts/manager/einoruntime 保留职责并验证嵌套适配；移除用户暂停/继续/整轮取消接口与控件，页面租约不再触发执行暂停，不实现活动消息注入或取消工具。HTTP/SSE 查询、认证及来源保护保留，不整体重写供应方客户端。旧暂停/取消历史继续只读保留，切换需明确解除旧非终态占用，不在新流程复活旧断点。

项目固定 Eino `v0.9.0-beta.1`，本提案不自动升级。现有 `add-general-web-search` 变更仍未归档，集成时需核对重叠文件和能力，不覆盖它的未完成事项。

## Non-goals

不重建平台或仓库，不按角色拆微服务，不做动态Agent发布、预订支付或通用协作引擎。Kafka用于本次子Agent调用分发，独立事件总线另行设计；不强制外部MCP服务理解Kafka，不默认叠加ZSET与延迟Topic。旧文档/数据保留，本轮不进入产品实施。
