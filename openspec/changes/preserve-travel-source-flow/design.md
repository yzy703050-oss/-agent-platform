## Context

见 proposal.md 的动机。正式行为仅由本变更 specs/ 及现行未修改规格定义。用户已确认沿用当前目录：Travel 属于 `agent/agents/`，与 General 并列；不新建差旅系统，不改变原版内部架构、设计或提示词。

基线：`G:/简历项目/差旅出行助手-v0/trip_assistant`，原始文件 SHA256 与静态提示值 SHA256 见 source-baseline.json。冻结范围含 93 个 Python 源文件，只读提取，不读取旧 .env、用户数据或断点。源码运行行为映射见 source-mapping.md；新发现遗漏先补映射及受影响任务，不能以计划措辞删减原版行为。

## Goals / Non-Goals

**Goals:** 用 Go 与现有 Eino 实现承载原版行为；恢复完整模型输入、工具协议、校正和真实收尾；保持平台公共底座和确定性业务边界。

**Non-Goals:** 本次不优化提示词、不引入新的智能路由/主 Agent 委派、不重写前端或 RAG、不移植 Python 存储/CLI/已退役单次生成实验，不建立另一个通用 Agent 框架。

## Decisions

### D1. 分层是职责和依赖约束，不增加一套系统

```text
Handler → Application/chat → AgentManager
                                  ├── GeneralAgent
                                  └── TravelAgent 实现
                                        └── Eino 差旅内核
                                              ├── Main / Verifier / Summary
                                              └── Model / Tools / Memory

Tools → agenttools/travel → Service → Repository / Client
执行日志、断点、上下文来源读取 → 消费者存储接口 → Repository
Model → 模型 Client → 现有模型服务
```

AgentManager 处理平台生命周期，不理解差旅策略。具体 Agent 适配平台接口并组装内核。Eino 内核承载原版角色和循环；共用 einoruntime 只负责运行/恢复。Service 处理业务是否合法，Repository 落实事务、围栏、证据和回执，Client 实现外部协议。Service 与 Agent 并列，Agent 不是所有数据的拥有者。

禁止 Agent 或 agenttools import postgres；禁止 Service import Agent/Eino；禁止模型自行提供平台身份、Run、attempt 或稳定操作 ID；禁止 Repository 决定重规划策略。bootstrap 组装实现。现有内部 package 拆文件即可，只有内核逻辑确实需要独立测试时使用同域小包，不改动顶层目录结构。

### D2. 同一个 TravelAgent 的角色切换

保留原版 `runtime/loop.py` 的决策顺序，而不是把每个角色注册成平台子 Agent：

1. 恢复已接受动作和待执行自动查询，再进入下一次决策。
2. 从原请求、上一批结果、当前全部行程和已有交接确定 Main/Verifier/Summary；政策或历史观察后的规划延续不能误入 Summary。
3. 准备源等价 review、候选视图、日历及 execution facts；先依原规则刷新允许刷新且有预算的证据。
4. 选择原版角色提示、共享 REPLY_POLICY 和角色专属工具 profile，再调用模型。
5. 对整批调用做源等价预检；接受决策落盘后，按回执执行独立动作及行程待查作业。
6. 原版有限修正、request_replan、无进展或停止分支继续生效；实际写回执满足绑定回复条件时可同轮收尾，不强制额外模型总结。

优先使用当前 Eino ChatModelAgent middleware、工具包装及 session state，恢复原版批次执行语义。实施前用一项窄探针验证整批预检发生在任何单项副作用之前，以及同轮 bound reply 能可靠停止。如果 Eino 当前接口无法满足，先在 design/计划记录具体限制和等价适配，不能暗改为逐项先写后校验或并行第二套决策循环。

### D3. 原文和工具契约一同保留

`agent/agents/travel/prompt.go` 负责选择原文，不再维护缩写 instruction。完整原文以该目录下纯文本资源或 Go 原文常量保存，五个静态常量和 TRIP_ARGUMENT_HELP 的值与基线 UTF-8 SHA256 一致。Main 已包含 rail policy，保留原拼接方式，不能重复添加。

模型可见工具恢复 `read`、`query`、`write_memory`、`write_trip`、`query_policy`，Verifier 增加独立 `request_replan`。保留 creation/pre-policy/review profile、nested tripplan、request_kind、reply、round_summary，以及原版 schema 的默认值、互斥和权限限制；不把 Go 内部扁平 TripCommand 直接当模型参数。

工具说明、Pydantic Field 描述、动态政策/偏好/压缩提示也是模型输入，不能只校验 prompts.py。静态内容逐字提取；动态内容用固定输入校验构成、示例和语义字段。源码摘要是冻结来源，不等于实现已通过。

### D4. 三种状态明确分开

| 状态 | 归属 | 内容 |
| --- | --- | --- |
| 平台 Run | AgentManager 与现有应用层 | 用户/会话、page、attempt、running/paused/终态 |
| 差旅执行 | Travel 内核，Eino session 序列化 | 原版 phase/role、业务批次预算、活跃耗时、review 指纹、无进展次数、交接、候选视图、待查与重试记录 |
| 行程业务 | Service/model，由 Repository 持久化 | 行程与任务修订、条件/来源/强度、内容、候选选择、校验、实际回执 |

恢复不丢角色、预算、无进展次数或已接受批次；操作标识跨 attempt 稳定，写入授权使用当前 attempt。原版执行阶段不直接映射为平台 paused/completed。

### D5. 业务规则迁到 Service，协议映射在工具边界

原版 contracts/conditions/strengths/activities/dependencies/patches/validation 的规则落在 `service/trip` 与无 Eino 的 model。支持 confirmed_conditions、任务 conditions、字段来源和强度、完整 route_tasks 与局部 components/content、allow_repair、候选引用和任务修订。保留遗漏、显式 null/清空和空列表的语义区别。

`agenttools/travel` 执行原版参数规范化和模型协议映射；Service 校验业务并返回结构化字段问题，不能用通用 invalid_input 抹去原因。供应方 Client 规范 G/D 车种等外部表示；Service 使用规范值匹配，不按中文名和字母直接比较。原版 CNY 与内部整数分在边界精确转换，溢出、负值和未知值明确拒绝或保留未知。

现有 Go 快照的平面字段保持可读，新原生契约快照标记 SourceVersion=1，增加条件/强度、任务修订、查询结果和约束快照。整条路线输入复用带指针的业务 patch 类型，保留省略与显式 false/[] 的区别，不直接用完整快照类型充当修改命令。可信决策角色由运行时传入，不进入模型参数；Main 接收新请求与 Verifier 自动校正的硬条件权限按原版区分。

同批不同计划写入可合法执行，但不是承诺整批跨计划事务原子；同一计划每批一次写、每个 Service 写事务原子。预检失败整批不产生副作用；执行时失败逐项记真实结果。明确偏好与规划可同批，政策与受影响规划不能同批。

### D6. 原版上下文、政策和记忆能力不能缩成空壳

review 使用原版字段与来源投影：original_query、全部 tripplans、calendar 所需条件、reply_facts、query_results、observations、memory_results、previous_round_summary、adjustment_history、execution_results。候选视图带当前结果/任务修订，过时结果不冒充新结果。压缩前过滤任务来源，保留完整工具配对。

政策仍走既有 RAG MCP：保留原版检索→模型提取一次→程序验证引用、适用范围、金额/席别规则→形成政策上下文，不把检索命中替代规则理解。提取模型角色放在差旅 Agent 域，确定性验证留 Service，模型调用不能塞入 Repository。偏好分析和压缩同样沿用原版触发条件与提示，使用 Go 平台允许的新数据；被替代任务的数据继续排除。这是宿主平台已确认的限制，不恢复 Python 的旧身份/存储体系。

### D7. 原版停止与绑定回复

使用原版业务投影指纹检测无进展，忽略回执/叙述变化；原版 unchanged_reviews >= 2 时停止。保留可用 partial 收尾判断，policy_unverified 不是让所有场景永远循环的统一阻断条件。Verifier 要实际 select/amend，不能靠“已检查”文字证明修改。

绑定回复从本次实际回执中的快照生成，支持 train/hotel/content 投影、局部修改及整条路线回复，活动必须可见。未知报价、地点数据和演示证据保持原边界，不创造出票/支付能力。独立查询保留所有所问结果，不能被行程渲染覆盖。

### D8. 执行限制保留原含义，与宿主围栏区分

原源码默认 planning_rounds=8、tools_per_batch=8、external_queries_per_batch=12、external_concurrency=3、supplier_concurrency=2、trip_write_concurrency=1、turn_timeout=600 秒、request_timeout=30 秒、extra_retries=2、memory_context_budget_tokens=20000。规划预算由 reserve_batch 累计，开始规划后按批次计数，不是固定“最多两次 propose/三次 amend”。

供应方沿用原版端点级滑动窗口熔断与单一重试负责人：failure_window=60 秒、failure_threshold=5、cooldown=60 秒、half_open_concurrency=1、recovery_successes=2。重试遵守原可重试错误集合、Retry-After 和剩余期限，不在 SDK、工具和 Client 叠加三套重试；取消释放所属作业。证据默认有效期 3600 秒，绑定及刷新仍由 Service 校验，不能恢复后自动延寿。

保留平台现有默认 MAX_ITERATIONS=8、RUN_SECONDS=120、TOOL_SECONDS=10 作为宿主限制，明确区分实际命中的限制原因；不偷偷扩大全平台预算。为 Travel 引入明确可配置的原版业务预算，并在验收记录宿主实际配置。若宿主限制先到，返回真实停止和可用结果，不宣称原版完整预算已执行。固定预算的执行/恢复测试证明原版计数语义，不能通过无限调大上限掩盖行为缺失。

### D9. 验证与交付

少量综合表驱动/脚本模型用例覆盖确定性语义，复用真实 PostgreSQL 随机 schema 和既有恢复 fixture，不迁入 Python 测试集。四类真实模型场景复用已有请求、供应方快照、原成功判据，每类 3 次。逐项核对数据库结果及角色/工具轨迹；如日期过期则只按固定相对偏移更新测试日期并记录，不调简单需求或放宽判据。

报告总调用与 Main/Verifier/Summary/政策/偏好/压缩等实际阶段调用，输入/输出 Token、耗时和工具数；无 Usage 不能记为零。明确测量不含 HTTP/SSE/UI，逻辑模型请求与底层 HTTP 重试不同。真实模型的非确定性不能以脚本模型测试替代；任何失败仍报告并保留修复任务。

## Risks / Trade-offs

- [原版参数模型比当前 Go 类型丰富] → 按源契约补业务模型，不在适配时丢字段；只添加必要迁移，保留旧记录读取。
- [Eino 工具并行默认行为与原批次预检不一致] → 先验证批次门禁及稳定回执探针，再接业务副作用。
- [提示词相同仍不保证模型输出相同] → 静态保真、脚本轨迹和实际业务验收分别提供证据，不宣称输出逐字一致。
- [宿主时间/迭代限制先于原版预算] → 分别记录和返回原因；配置变更显式可审查，不连带改变 General。
- [跨业务提交和断点保存存在中断窗口] → 继续复用现有事务回执和围栏，扩展恢复场景，不能绕过它们追求源码外形一致。

## Migration Plan

在现有工作分支、本项目内单代理实施。新增字段先兼容读取已有 Go 数据，不重写历史，不导入 Python 存储；只有确需数据库结构变化才追加迁移。更新差旅工作流/提示/schema 指纹，旧断点拒绝继续但历史可见，不强制反序列化。保留 General 和平台安全暂停回归。完成任务、测试、自审、规格人工核对后再 archive；现有 CLI 无 verify 命令，不声称运行 OpenSpec verify。
