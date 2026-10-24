# 原版差旅保真迁移实施记录

正式依据：`openspec/changes/preserve-travel-source-flow/`；详细计划：`docs/superpowers/plans/2026-10-24-preserve-travel-source-flow.md`。用户于 2026-10-24 审阅并批准按计划单代理实施。保持当前目录及非主分支原地迁移。

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
