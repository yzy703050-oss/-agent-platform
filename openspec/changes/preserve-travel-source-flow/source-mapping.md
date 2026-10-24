# 原版行为与 Go 分层映射

本表是迁移追踪，不是另一套需求。行为依据 specs/，技术边界依据 design.md。源文件及静态提示基线见 source-baseline.json；映射的“目标”是待实施落点，不代表当前已经完成。

## 提示基线

通过 Python AST 读取字符串字面量、名称引用与字符串相加，未 import 或运行原项目。以下字符数只用于定位，验收使用 JSON 中的完整 SHA256。

| 源位置 | 提示值 | 字符数 | 目标责任 |
| --- | --- | ---: | --- |
| agents/prompts.py | REPLY_POLICY | 303 | Travel 全阶段共享回复边界 |
| agents/prompts.py | RAIL_PLANNING_POLICY | 264 | 原样包含在 Main 原文中 |
| agents/prompts.py | MAIN_PROMPT | 1375 | Main 角色 |
| agents/prompts.py | TRIP_REVIEW_PROMPT | 1055 | Verifier 角色 |
| agents/prompts.py | SUMMARY_PROMPT | 216 | 独立查询总结 |
| tools/registry.py | TRIP_ARGUMENT_HELP | 1015 | agenttools/travel 工具说明 |
| agents/policy.py | query 内动态 prompt | 动态 | 差旅政策提取角色与固定输入 golden |
| agents/preference.py | analyze 内动态 prompt | 动态 | 差旅偏好分析与覆盖来源校验 |
| memory/compaction.py | summarizer 内动态 prompt | 动态 | 只压缩完整已结束轮次 |
| tools/registry.py、trip_contracts.py、trips/contracts.py、parameter_schema.py | 工具/字段描述与 profile | 动态 | 模型可见 schema 与服务端同等验证 |

## 行为映射

| ID | Python 来源 | 必须保留的行为 | Go 目标层 | 验证任务 |
| --- | --- | --- | --- | --- |
| F01 | agents/prompts.py、main.py | 完整角色原文和共享策略，普通问答、默认往返、显式单程、完整路线 | agent/agents/travel/prompt.go、agent.go | 1、4 |
| F02 | runtime/loop.py、state.py、review.py | 原角色决策顺序、合法 phase、Verifier→Main 交接及有限停止 | Travel 内核，不经过 AgentManager | 4 |
| F03 | tools/registry.py、schema.py、trip_contracts.py | read/query/write_memory/write_trip/query_policy、profile、request_replan、字段默认/互斥 | agenttools/travel | 1、3 |
| F04 | runtime/execution.py、actions.py、scheduler.py | 全批预检、同计划防重复写、合法独立动作并发、原生工具配对 | Travel 内核批次门禁＋工具适配，现有 journal | 3、5 |
| F05 | runtime/trip_jobs.py、trips/query_execution.py、discovery.py | 行程查询地图、自动待查作业、真实依赖与恢复，独立查询不重复 | Service 查询计划＋Travel 有界作业适配 | 2、3、5 |
| F06 | trips/contracts.py、conditions.py、strengths.py、parameter_schema.py | 条件分层、来源/强度、默认与用户明确值、精确金额与时间 | model、service/trip | 2 |
| F07 | trips/activities.py、dependencies.py、patches.py | 目的地活动、粗时段、组件更新、整路线修改、保留未受影响结果 | service/trip | 2、6 |
| F08 | trips/service.py、validation.py、constraints.py、conflicts.py、state.py | 原子业务操作、硬约束/跨段/逐晚住宿校验、原版部分结果状态 | service/trip＋Repository 事务 | 2、5 |
| F09 | providers/rail_preferences.py、validation.py、contracts.py、candidates.py、views.py、plan_guard.py、budget.py | 规范车种、候选身份与视图、条件匹配、分页授权和金额未知 | Client 规范化＋Service 证据校验 | 2、3 |
| F10 | providers/juhe_train.py、amap_hotel.py、arrival_search.py、hotel_places.py、public_query.py、base.py | 供应方能力与错误边界，地点不等于价格/库存 | 既有 client/train、hotel、weather 等，按源码实际文件核对 | 2、6 |
| F11 | agents/policy.py、tools/policy.py、providers/modular_rag.py、rag.py | MCP 检索、一次模型提取、引用和适用范围验证、缺口保留 | Travel 政策角色＋Service＋client/mcp | 4、6 |
| F12 | agents/preference.py、tools/memory_write.py、storage/preferences.py | 明确更新/撤回、用户要求重分析、每 3 个新确认有效行程的低频分析及证据覆盖 | Travel 记忆能力＋service/travelpreference | 4、5 |
| F13 | memory/planning.py、context.py | 所有行程 review 投影、原请求、候选视图、日历、调整历史、查询和执行结果 | agent/memory 的差旅上下文＋Service 只读快照 | 4 |
| F14 | memory/compaction.py、llm/budget.py、messages.py、structured.py | 完整配对、80% 阈值、最多三个完整前缀压缩、结构化解析和模型输入预算 | 差旅上下文/模型适配，复用平台存储接口 | 4、5 |
| F15 | runtime/limits.py、progress.py、errors.py、evidence_refresh.py | 业务批次预算、活跃时间、具体错误、允许的证据刷新、失败后受限重试 | Travel 状态＋Service 规范错误与查询 | 3、4、5 |
| F16 | runtime/state.py、presentation/trip_reply.py、facts.py | 无进展检测、实际回执判断、组件绑定回复、停止时保留事实 | Travel 内核＋service/trip 回复投影 | 2、4、6 |
| F17 | runtime/recovery.py、storage/actions.py、evidence.py、trips.py | 决策/动作/查询作业的恢复语义，成功写入不重放、未知结果不自动猜测 | einoruntime、agent/ports、Repository | 5 |
| F18 | runtime/telemetry.py、resilience.py、providers/resources.py | 分阶段调用/Usage/耗时、取消、供应方并发和有限容错 | 现有模型/Client 包装＋本次测量入口 | 3、6 |
| F19 | settings.py、bootstrap.py | 运行参数有明确语义、依赖集中组装 | config、bootstrap，区分宿主和原版业务限制 | 4、5 |

## 宿主替代与明确排除

| 源模块 | 处理 | 原因 |
| --- | --- | --- |
| storage/sqlite.py、locking.py、index.py、sessions.py | 持久化语义由现有 PostgreSQL Repository 实现 | 用户要求统一存储，不复制 SQLite |
| storage/identity.py、migration.py、legacy_mirror.py | 不导入旧身份或用户数据，不建立镜像迁移 | 已确认从 Go 新数据开始 |
| cli.py、runtime/logging.py 的 CLI 展示 | 入口及展示由现有 HTTP/SSE/React 负责，保留必要进度与错误 | 已有宿主平台 |
| __init__.py、兼容 import 等 | 不要求逐文件建立 Go 对应文件 | 保留可观察行为，不保留 Python 包装外形 |
| 退役 single_pass_planning | 继续拒绝，不恢复 | 原源码已明确退役 |
| Python 旧测试、旧评测结果、旧配置密钥 | 不复制、不当作 Go 验收证据 | 用户只要求关键测试和 Go 实际效果 |

## 当前已证实的偏差

- Go prompt.go 为缩写指令，未使用五个原文提示值。
- Go 工具名称和扁平参数与原版不同，缺 creation/review profile 与 request_replan。
- Go 只有 planning/querying/validating/reply，不等价于源执行状态及角色职责。
- Go 额外限制两次 propose、三次 amend、每模型轮一次修改；不等价于原版业务批次限制。
- Go 错误压成通用 invalid_input，丢失可校正字段信息。
- Go 高铁条件与 G 车种直接比较；原版使用 high_speed/emu/other 规范值。
- Go 缺原版完整 review 投影、语义无进展判断和组件绑定回复，现渲染不含活动。
- Go 政策主要做片段适用判断，未恢复原版政策模型提取和完整规则核验；低频偏好分析与压缩需恢复源触发和提示。

以上是源码证据，不代表每次模型失败的单一原因。性能测量未保存完整调用参数/观察，不能据此断言全部根因。后续失败需在授权、脱敏范围记录角色、规范参数、校验问题和实际状态变化再诊断。
