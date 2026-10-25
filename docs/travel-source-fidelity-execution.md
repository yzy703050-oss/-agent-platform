# 原版差旅保真迁移实施记录

正式依据：[已归档变更](../openspec/changes/archive/2026-10-25-preserve-travel-source-flow/)；详细计划：`docs/superpowers/plans/2026-10-24-preserve-travel-source-flow.md`。用户于2026-10-24审阅并批准按计划单代理实施。保持当前目录与已有分支原地迁移，时间顺序记录中的“待验证”以文末最终结果为准。

## Task 1：原文与契约基线

- BASE：eb85407。初始受影响 Agent/Tool/Trip 三个 package 单测通过，真实模型旧失败场景没有因此消失。
- RED：TestTravelSourcePrompts 证实当前缩写 Main SHA256 不符，另外四份资源缺失；TestTravelSourceContracts 因没有 source profile 入口失败；NativePatchPresence 因缺少源参数类型失败，追加非法尾随 JSON 的行为断言也先失败。
- GREEN：五份原文按 AST 求值后字节导出并 embed，五个 SHA256 与冻结基线一致；TRIP_ARGUMENT_HELP 原样保留。SourceProfile 以源 ToolDefinition.schema 导出 main/pre_policy/review/review_retry/summary，完整工具说明和 schema 经过 Eino JSON Schema 转换后仍与原值一致。
- 固定了政策/偏好/压缩三个动态提示的固定输入 golden。它们是待 Task 4 消费的基线，不代表辅助模型角色已恢复。schema 导出仅加载原项目类型模块，不启动 Application、不读取其 .env 或数据；禁写 Python bytecode，使用本机 pydantic 2.13.4，未安装依赖。
- Native 参数类型保留 nested tripplan、整数修订和局部 patch 字段存在性，明确区分省略 content 与 content=[]；完整验证和业务映射留 Task 3。
- 添加精确资源的 Git 字节保护，避免 Windows checkout 改换行导致原文摘要失配。
- 验证：`go test ./internal/agent/agents/travel ./internal/agenttools/travel ./internal/service/trip -count=1`，退出 0，三个 package 通过。原始日志在 `.cache/execution/travel-source-flow/task1-*.log`。

## Task 2：业务语义与绑定回复

- 新原生业务快照 SourceVersion=1，保留条件/来源/强度、任务修订、查询项目、约束快照和原版停止原因；兼容已有 Go 数据字段。整条路线使用带指针的 patch 输入，省略住宿等字段不会变成清空。
- 恢复 Main 处理新修改与 Verifier 校正权限的区别；Verifier 不能改变已有硬活动、硬条件和路线，返回 REPLAN_REQUIRED；活动时间改动同步已有描述中的旧时间，保留无关车次，修复代码必须引用当前相关问题。
- 完整角色和工具仍未接线，这些业务用例此时通过 Service 的纯 Preview 验证，不代表已经经过真实 PostgreSQL 或真实模型执行。
- 原版高铁分类按完整车次规范为 high_speed/emu/other/unknown；Service 可识别旧 Go 的 G/D 表示。金额经精确有理数转换整数分，人数乘价和每晚预算不使用浮点或整数截断。
- 恢复酒店地点价格未知的有条件阻断、粗时段重叠不确定性、真实日期/跨段时间比较、当前候选证明的硬日期冲突；无候选或存在可行候选时不误认全部不可能。已证实的硬日期冲突不启动无用查询。
- 绑定回复验证本次实际回执的行程/修订及 select/amend，支持 content 局部回复和路线整体展示；没有回执、旧修订、proposal 回复、错误组件引用均拒绝；不把模型解释作为事实。
- RED→GREEN 覆盖：规范车种、14:00→16:00、局部组件范围、整路线省略值、仅查询新 items、硬预算继承、报价需求证据、粗时段、硬日期冲突、人数金额、每晚不足一分的边界、具体错误、回执与回复范围。按综合用例组织，不移入 Python 测试集。
- 验证：`go test ./internal/... -count=1` 退出 0；受影响 Trip/Train/Tools/Travel 四个 package 重跑通过；对应 `go vet` 退出 0；`git diff --check` 退出 0。原始日志 `.cache/execution/travel-source-flow/task2-*.log`。

Task 1/2 的阶段检查完成。工具实际接线、完整 Eino 角色编排、政策/偏好/压缩辅助模型、恢复和真实模型验收仍未完成，不得据此声称整次重构完成或原版效果已经恢复。

## Task 3：原生工具、整批门禁与查询作业

- 工具恢复原版 main/pre_policy/review/review_retry/summary 协议；规范化只处理无歧义格式，保留精确金额、字段存在性及原失败查询范围。业务 Prepare 只读校验整批；相同计划两写、政策与规划混批等在任何业务副作用前拒绝。
- 真实 Eino 探针验证整批预检先于工具并发、逐 call_id 观察配对；journal 接受合法的不同计划写入，未知工具同样记录拒绝。实际 bound reply 经确定性模型节点输出，底层模型计数不增加。可信角色上下文经过 session 序列化后仍保留。
- 查询作业由实际写回执派生，使用稳定父操作 ID；独立于模型工具调用记录，按依赖分波执行、并发上限三，逐项记录后才放行后续依赖；完成结果可重建后跳过。数据库原子排队/重启恢复仍由 Task 5 验证，不能把内存 fixture 当数据库保证。
- 原始查询缓存与调用者筛选/排序分开，原生证据有效期一小时；显式刷新可重查，抵达日期搜索最多三个出发日期，未知日期证据停止搜索。候选视图最多五项，未知硬证据排在已核实候选后，引用选择验证当前任务修订和可见候选。
- Client 是唯一 HTTP 重试负责人：源错误集合、额外两次、Retry-After 与期限、端点级 60 秒窗口/五次失败/60 秒冷却/单半开探测/两次成功恢复。Service 不再叠加供应方重试；测试采用注入时间与睡眠，无付费接口。
- 用户要求不单独保留 adapter.go：Travel 平台实现为 agent.go，Eino 组装为 kernel.go；工具命令转换为 commands.go，无额外 Adapter 类型或调用层。
- RED→GREEN 包括缺失熔断接口、查询投影/缓存、journal 原多写限制及未知调用记录、批次序列化丢失可信角色、空父操作 ID 作业。查询作业依赖/完成重放测试为综合测试，没有移入原 Python 测试集。
- 验证：受影响 AgentTools、Travel、Trip、全部 Client 单测与 go vet 退出 0，日志 task3-*.log。此阶段仅验证独立执行模块；生产 Agent 接回完整角色、政策/偏好/压缩和安全恢复尚由 Task 4/5 完成，真实模型指标尚未重跑。

## Task 4：实施中

- 已将生产 Agent 构造接到原生 Registry 和同一 Eino 内的角色链路。脚本模型＋真实 Eino 已覆盖 Main→查询→Verifier→保存绑定回复，以及查询→Summary、政策观察→Main、重规划回 Main、双计划审查、无进展停止；均不代表真实模型验收。
- 三类动态辅助提示固定输入逐字一致；政策单次提取及引用/适用范围/单位验证已接入，MCP 元数据保留。偏好三条新增有效确认行程的触发、覆盖校验、明确与撤回保护已有模块测试。
- 差旅完整来源过滤历史先交给自身的 80%/最多三个完整前缀压缩，General 原有上下文策略不变。真实数据库偏好批量提交、历史读取和查询作业事务尚待 Task 5。
- 当前仍在补证据刷新、模型不完整响应和恢复衔接，阶段未完成，未做本轮真实模型效果/Token验收。

Task 4 阶段验证：原版角色与动态 profile 已接线；真实 Eino＋journal 脚本覆盖普通 Summary 补查、单次政策提取、重规划、多个计划、原版无进展指纹、过期证据审查前及迟到刷新、再次过期停止、无效模型响应再生成与 review 重入。独立相关事实按原版收尾路径追加；普通 Summary 文本保留，平台仍禁止以无预订/付款回执的模型文字声称预订付款成功。全部 internal 单测与 go vet 退出 0（task4-internal-final.log、task4-vet.log）。本阶段证明 Agent/Service 模块接线，不替代 Task 5 数据库回执、历史/偏好批量事务与恢复验证；Task 6 真实模型指标尚未重跑。

## Task 5：真实 PostgreSQL 与恢复

新增 005 迁移（不改写 001–004）：原生偏好批量操作支持每键唯一修订、source_runs 依赖和同事务回执；新增 travel_query_jobs，与源行程提交一起排队。实际证据、作业完成回执和该组查询观察写在同一事务；完成作业重建后复用，运行中无结果的作业转 unknown，禁止自动重查。每组查询结束后新增一份不可变行程修订，Repo 只附加实际观察，校验仍由 Service 处理。005 降级涉及一操作多键，不执行隐式有损降级。

已接受的模型决策保存完整原始输入与源执行快照；重建时复用该输入、角色、预算和权限，避免当前时间变化造成幂等哈希冲突。原始模型决策与实际展示文本分别保留。只有已接受操作的实际回执能够跳过过时的业务预检；其他输入仍走完整预检。工作流指纹升级为 source-flow-v2，旧断点拒绝恢复、保留历史。

安全暂停期间允许只读预检和已开始查询作业的当前 attempt 结果提交；新业务写入和新查询领取仍要求 running。历史展示使用传统 Read，Agent 读取另加 source context_eligible 围栏；混合偏好/摘要验证所有依赖来源。

RED→GREEN：原生偏好批量端口缺失、精确批量回执/来源过滤、查询结果丢断点重建、同批首计划提交后失联再构造 Agent 完成第二项、实际模型输入排除旧任务、动态时间导致已接受决策哈希变化、Eino 实际输出仍流出原始假付款文字、错误任务引用触发刷新。对应脚本与 PostgreSQL 随机 schema 测试通过。既有工作流夹具改用源 tasks/selection_refs；查询观察单独形成修订，金额/箭头断言按源回复格式表达相同事实；Service 测试改为断言不叠加 Client 重试，HTTP 重试仍由专门 Client 测试验证。

验证：REQUIRE_DATABASE_TESTS=1 下 go test ./... -count=1 -timeout 5m 退出 0，集成包 36.012 秒；go vet ./... 退出 0。包含真实暂停/手动继续、页面切换/关闭、General 恢复回归。task5-all-final.log、task5-vet.log；真实模型及性能验收尚待 Task 6，不能把脚本成功当作模型效果恢复。

## Task 6：真实模型验收与计量

原版生成参数和所有角色提示保持原样。travel.model_call 包装实际 Generate，包含辅助模型、失败及 Usage 缺失；已接受决策重放不计新调用。少量综合回归覆盖全阶段计量、JSON 参数语义身份、原生历史、旧 Go 活动修改、真实 HTTP 生成参数、软推导期限、具体且安全的 schema 反馈。

真实模型复核又发现 review 重复完整审计、上一轮摘要结构不符、JSONB 键顺序假进展和宿主迭代门禁早于安全点收尾；均先有失败回归再修复。当前 review 只传源业务投影和引用，完整审计保留，达到宿主限制的确定性收尾不额外调用模型。演示供应方的余票说明文字改为规范 unknown，避免被误判为明确不可售；未提供任何真实余票、报价或库存保证。移除仅服务旧简化协议的 Kernel fallback 与旧 checkpoint 夹具，真实 PostgreSQL 源流程恢复测试保留且通过；生产指纹 flow-v4/batch-v4 拒绝不兼容旧断点。

最终同代码 round6：查询、单程、多城、局部活动修改各3次，12/12通过；集成命令122.075秒。本轮全部 Usage 完整。前五轮依次0/12、5/12、7/12、9/12、11/12，未删失败或挑最好样本。四次定向探针也保留，总计76份脱敏样本。完整逐次 Token、阶段调用与延时、供应方次数和实际业务结果见 [验收记录](travel-source-fidelity-verification.md) 及其 JSON；不以运行无 error 代替三段/住宿/活动保存或16:00修改断言。

当前完整 go test ./... -count=1 -timeout180s（真实 PostgreSQL）退出0，集成包36.546秒；go vet和server构建退出0；前端2文件15项测试和生产构建退出0。日志task6-backend-round6、task6-vet-round6、task6-model-round6、task6-frontend-round5、task6-frontend-build-round5。Windows race二进制0xc0000139加载失败，不能报告通过；Linux CI尚待当前提交验证。

## Task 7：单代理自审与人工规格核对（进行中）

按用户已选单代理执行，由当前实现者整体自审，未另派独立评审。核对模块依赖：Service不导入Agent/Eino，Agent和工具不导入postgres/pgx，业务写仍经Service；执行/查询结果与回执通过消费者接口和Repository事务。逐项核对specs的场景、F01–F19映射与下面证据：

| 需求/风险 | 代码与验证证据 |
| --- | --- |
| 完整原文、阶段工具契约及字段存在性 | TestTravelSourcePrompts、TestTravelSourceContracts、TestTravelNativePatchPresence；93个Python文件SHA复核全部一致 |
| 多城/单程/活动/修改、规范化和硬约束 | TestTripSourceRules/Conditions/Boundaries、TestTripBoundReply、TestTravelSourceQueries、TestTravelSourceModel真实保存结果 |
| 政策、偏好与来源隔离/压缩 | TestTravelSourceAuxiliary/Execution、TravelMemoryIsolation及source recovery；无旧Python用户数据导入 |
| 无进展、部分收尾、完整review | TestTravelSourceFlow/Scenarios/Eino、TestSourceReviewProjection、TestSourceProgressCanonicalizesJSONB、TestTravelEinoBatchGate |
| 整批预检失败零副作用、合法独立并发、权限 | TestTravelSourceBatch、TestTravelEinoBatchGate、TestTravelSourceJournal |
| 提交后断点前故障、作业复用、旧attempt和checkpoint | travel_source_recovery_test.go、既有Eino恢复/页面生命周期/HTTP租约测试，真实PostgreSQL |
| 所有模型阶段计量及失败披露 | TestTravelMetricsAccounting、真实模型四组12次、全部76份历史与最终脱敏计量 |

自审修复集中于以上实际模型发现及对应RED→GREEN，没有待掩盖的关键失败；历史README的32KB差旅预算、缺005迁移和RAG“没有命中”等陈旧描述已修正。原生Travel继续使用agent.go平台实现、kernel.go Eino装配、commands.go协议转换，没有独立adapter.go。当前OpenSpec CLI无verify命令，已运行strict validate退出0并人工逐条核对；Linux race及最终归档尚未标完成。外部真实供应方/RAG在线、浏览器和Docker部署本轮未重验，不作为本次通过证据。

2026-10-25最终复核：dee12d8ae6b5ac8dc33a444cc3c42a1570f039be已推送。[CI 40099473344](https://github.com/yzy703050-oss/-agent-platform/actions/runs/40099473344) backend/frontend均success；实际步骤包含Linux go test -race ./... -count=1 -timeout180s及PostgreSQL、vet/build、npm测试/build。完整backend日志保留task6-linux-ci-dee12d8.log。本机Windows race失败仍如实保留，现有Linux结果解决该发布验收缺口。Task6的全部完成条件满足，Task7 strict validate及人工规格核对已通过；下一操作仅为同步规格与归档。

分支收尾采用用户既有推送授权，保留feat/agent-platform-mvp；仓库默认分支本身就是该分支，且没有独立main/master，因此不擅自造一个基分支或创建自身到自身的PR。不删除工作目录，也不合并其他分支。所有业务改动停留在已通过CI的dee12d8，归档提交只包含文档。

2026-10-25归档成功：OpenSpec将travel-execution/travel-planning的修改同步到主规格，新增travel-source-fidelity，共新增5项需求、修改3项。归档路径为openspec/changes/archive/2026-10-25-preserve-travel-source-flow。CLI当时12/13的唯一未完成项是7.2“执行归档”自身，非遗漏实施或测试；已按实际成功结果更新。最终分支保留并推送，后续文档提交不改变通过CI的代码。

### 执行中的技术决定与代价

- 复用当前分支和目录，不另建工作树，符合用户在本项目单代理迁移的要求；代价是需要依赖明确提交范围隔离改动。
- fixture JSON换行统一LF，验证语义一致后更新字节基线；代价是fixture文件摘要与第一次Windows导出不同，提示原文字节未改。
- 整路线输入采用带指针的TripTaskChange，保留省略与显式false/[]；代价是工具边界需要明确转换。
- 不保留adapter.go，agent.go实现平台接口，kernel.go装配Eino，commands.go只做协议映射；代价是这些职责仍在同一Travel包内，需要继续维护同包边界。
- 宿主MAX_ITERATIONS仍限制主决策，辅助模型和有限协议再生成另计但计入总调用/Token且共享Run期限；代价是实际总模型调用可超过主循环次数，报告不能把二者混称。
- 原版普通Summary保留Main工具权限，只有停止后的只读收尾禁用工具；已有平台无预订/付款回执的事实保护继续适用，代价是最终展示必须同时遵守平台交易能力边界。
- 查询作业状态不为每次claim写业务修订，整组实际查询观察形成一份不可变新修订；代价是修订数比旧MVP更高，展示和测试需要区分保存与查询观察。
- 安全暂停允许只读预检与已领取的当前attempt查询结果提交，禁止新业务写/新查询claim；代价是暂停过渡期可能还有已开始I/O回执落盘，不能把pause请求当成瞬时切断所有工作。
- 按用户选择完成单代理整体自审，不另派独立评审；代价是缺少新上下文评审者，保留真实数据库故障窗口、全套回归和Linux race作为可核验的交付证据。
