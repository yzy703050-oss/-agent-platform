# 差旅 Agent Go 重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. 用户已明确选择单代理执行，按任务连续实施并整体自审，不派逐任务子代理。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Go 平台中实现独立 Travel Agent，迁移差旅核心能力并复用平台执行与数据访问。

**Architecture:** Handler/Application 统一提交给 AgentManager；Travel 通过 Eino ChatModelAgent Handlers 编排，工具调用 Trip/Preference Service；Repository 实现事务存储，Client 对接查询供应方及用户已有 RAG MCP 服务。General 与 Travel 共享必要的 Eino TurnLoop 适配，各自隔离可变执行状态。

**Tech Stack:** 当前 backend Go module（go 1.26.0），Gin v1.12.0，Eino v0.9.0-beta.1，模型扩展 openai v0.1.12，pgx v5.11.0，现有 React/TypeScript/Vite 前端及 PostgreSQL 17.11。

**Spec:** `openspec/changes/refactor-travel-agent-go/` 下的 `specs/`、`design.md` 和 `tasks.md`。正式行为仅以该 OpenSpec 变更为准；源码迁移矩阵 B01–B15 仅用于追踪覆盖。本计划不另立需求。

## Global Constraints

- 直接在当前项目和非 main/master 工作分支实施，不新建 Go module、差旅进程、迁移仓库或工作树；用户明确要求在本项目内迁移和测试。
- 只有 general、travel 两个独立入口，不做 General → Travel 委派。
- 不导入原 Python 行程、偏好、历史、摘要或断点，不删除原项目或现有平台数据。
- Service 不依赖 Eino 或 Agent，业务工具不绕过 Service 写 Repository；执行数据仍经现有 ports。
- 每个活动 attempt 120 秒、工具 10 秒、暂停 15 秒、模型迭代上限 8；恢复不重置差旅累计迭代限制。
- 数据库迁移只追加在 003 后；集成测试只写 TEST_DATABASE_URL 下随机 schema，不在开发业务表写测试数据。
- 不移植 Python 上千个测试，不以数量或覆盖率为 KPI；复用平台回归，相关规则合并为表驱动或综合用例。
- 开发只运行受影响检查；交付前统一完整验证。真实模型和供应方冒烟有代表性即可，不在每次单测调用付费服务。
- 当前未提供 RAG 服务和供应方配置；先实现内部接口与契约测试，真实接通不齐备时不得勾选真实接入验收，也不得用模拟行程替代。

## Review Focus

1. 业务提交成功但后续断点未保存：任务 1/3/6 共用故障场景，恢复必须读回执，不能重复修订。
2. 暂停/取消先完成状态迁移：任务 3 覆盖旧 attempt 与非 running 的业务拒绝写入。
3. 被替代任务通过偏好或混合摘要重新泄露：任务 5 直接断言下一次模型输入，不能只检查数据库标志。
4. 模型编造候选或外部查询迟到：任务 2/4 证明绑定、修订、时效及硬约束拒绝，不在每一层重复规则测试。
5. 一个 Agent 未配置或 MCP 失败：任务 7/8 分别验证 Agent 可用性与政策未知，不阻断另一入口、不宣称合规。

## 工作与证据约定

命令除特别说明均从 backend 执行。先在项目根运行 `. ./scripts/load-env.ps1`，仅将配置加载到进程，不打印凭据；设置 `REQUIRE_DATABASE_TESTS=1` 后进行数据库验收。使用现有 .cache/go-mod 和 .cache/go-build。新任务测试输出保存到本计划的执行记录目录，阅读实际退出码和必要尾部；不重复打印整套日志。

每项任务遵循 RED → GREEN → 整理 → 受影响回归，并更新 OpenSpec tasks 与本计划执行记录。首次未定义符号/契约造成的构建失败可证明新能力缺失，随后必须出现真正的行为断言；不能把编译通过当作最终验收。提交仅包含对应任务文件和文档，不包含 .env、缓存或原 Python 文件。最终通过前不归档。

## 文件与类型约定

下面签名是任务之间的工程契约，允许因探针结果修订，但必须先同步 design/本计划并记录原因。业务类型均放在 model，且不 import Eino。具体结构字段按相应 Task 固定，不将模型给出的身份、价格或“已完成”状态当作可信执行事实。

- `model.TravelExecution`：Scope、ConversationID、RunID、Attempt，均来自平台。
- `model.TripPlan`：ID、UserID、ConversationID、Revision、Tasks、Constraints、Validation；业务快照独立于 Run 状态。
- `model.TripCommand`：Operation（propose/select/amend）、PlanID、ExpectedRevision、Tasks/变更、候选引用；服务端另传逻辑 OperationID。
- `model.TripResult`：Plan、Validation、Receipt；最终事实来自实际提交结果。
- `model.TravelQuery / TravelEvidence / TravelCandidateRef`：查询领域及规范化条件、证据来源/时间/失效时间、query/result revision 与候选 ID；金额使用精确最小单位及明确空值。
- `model.TravelDecision`：RunID、DecisionKey、Stage、RequestHash、Response（平台 Message）、稳定 OperationIDs；不保存 Key 或原始连接配置。
- `model.TravelOperation`：TravelExecution、OperationID、RequestHash；attempt 围栏与稳定逻辑操作标识分开。
- `model.TravelPreference / TravelSummary`：值/撤回、修订、明确或推断来源、来源 Run 集合，不能只有不带来源的最终字符串。

### Task 1: Eino 多阶段恢复探针与最小共享运行适配

**OpenSpec:** tasks 2.1、2.2 的技术部分，6.3；travel-execution 的安全暂停、手动恢复和启动无副作用。

**Files:** 创建 `backend/internal/agent/einoruntime/{runner,checkpoint_store}.go`、`backend/internal/agent/agents/travel/{agent,state}.go`、`backend/internal/agent/agents/travel/recovery_test.go`；修改 general/{runner,checkpoint_store,agent,repository_store}.go 的共享依赖，保留现有 General 测试和对外行为。

**Interfaces:** 共享 `NewRunner(ctx context.Context, cfg RunnerConfig) (*Runner,error)`、Run/RequestPause/Result 与当前 General 一致；Travel 的 `NewKernel(ctx context.Context, cfg KernelConfig) (adk.ResumableAgent,error)` 接收 Model、Tools、固定执行身份、Handlers/状态存储依赖。阶段状态从 Eino SessionValues 读写，带工作流版本与累计模型计数，不依赖存活的 Go 对象。

- [x] 先写 `TestTravelCheckpointResume`：脚本模型完成阶段一工具后阻塞下一步，请求暂停，重建模型/Agent/Runner，仅从持久化 bytes 恢复；断言阶段和计数保留、已完成工具不重跑。子场景覆盖不兼容断点和保存失败，复用测试 fixture，不复制三套框架。
- [x] 运行 `go test ./internal/agent/agents/travel -run TestTravelCheckpointResume -count=1`。Expected: 新内核/阶段恢复缺失导致失败；记录实际失败原因。
- [x] 最小提取现有 TurnLoop，不改变其停止确认规则；实现 ChatModelAgent Handlers 阶段注入、动态工具可见性与执行授权、完整决策边界。先使用非流式决策；不调用真实供应方。
- [x] 重跑新用例及 `go test ./internal/agent/agents/general ./internal/agent/einoruntime ./internal/agent/agents/travel -count=1`。Expected: PASS，无数据库或真实模型前置条件。探针如不能成立，停止相关内核实现并更新设计，其他纯业务任务仍可推进。
- [x] 自审并提交 `refactor: share Eino runtime and verify travel recovery boundaries`，记录证据。此任务不单独证明真实业务幂等，2.2 在任务 3/6 补齐前保持未完成。

### Task 2: 行程类型、规则与修改语义

**OpenSpec:** 3.1–3.3；travel-planning，travel-evidence 候选校验；B01–B04、B06。

**Files:** 创建 model/{trip,travel_evidence}.go，service/trip/{service,validation,constraints,query,ports}.go 与 `service/trip/service_test.go`。

**Interfaces:** `trip.Preview(previous *model.TripPlan, cmd model.TripCommand, evidence []model.TravelEvidence, now time.Time) (model.TripResult,error)` 为无 IO 规则入口；`Service.Apply(ctx, ref model.TravelExecution, operationID string, cmd model.TripCommand) (model.TripResult,error)` 通过 Store 提交；`Service.Read(ctx, scope model.Scope, planID string) (model.TripPlan,error)` 受归属检查。Store 提供 GetTrip、CommitTrip，完整字段在 Task 3 实现。

- [x] 先写一个表驱动 `TestTripRules`，覆盖有效多段/返程、缺必要条件、日期冲突、硬约束不能降级、活动修改保留未影响选择、日期变化使相关选择失效、伪造/过期候选和未知价格；断言结果与查询需求，不镜像内部函数。
- [x] 运行 `go test ./internal/service/trip -run TestTripRules -count=1`。Expected: 缺失规则而失败，随后实际规则断言能捕获违规输入。
- [x] 按当前 Python contracts/validation/dependencies 的行为重写 Go 规则；日期按 Asia/Shanghai 解释，业务 ID 由服务端生成；严格校验模型字段和来源，不把模型输出直接视为持久化结果。
- [x] 运行 `go test ./internal/service/trip -count=1`。Expected: PASS；无 Eino import，无 PostgreSQL 或网络依赖。
- [x] 自审并提交 `feat: implement travel planning rules and incremental changes`，更新 3.1–3.3 的实际完成范围。

### Task 3: 差旅迁移、业务事务与决策回执

**OpenSpec:** 4.1–4.3、2.2 的真实事务部分、6.4；B09–B10。

**Files:** 创建 migrations/004_travel.up.sql、004_travel.down.sql；修改 repository/postgres/db.go 迁移列表；创建 postgres/{trip,travel_evidence,travel_execution,travel_preference}.go、model/travel_execution.go、tests/integration/travel_test.go；扩展 agent/ports 的决策存储契约。

**Interfaces:** Repository 实现 `GetTrip(ctx,scope,id)`、`CommitTrip(ctx, in model.TripCommit) (model.TripResult,error)`、`FindTravelDecision(ctx,ref,key) (model.TravelDecision,bool,error)`、`AcceptTravelDecision(ctx,ref,decision) (model.TravelDecision,error)`。TripCommit 包含可信执行、稳定操作标识/摘要、预期修订及已验证候选；写入事务仍必须重新检查绑定和 Run 围栏。

- [x] 在现有集成测试 fixture 中写 `TestTravelTransactions` 综合场景：从 003 升级、重复迁移、旧平台数据保留；两个同修订更新最多一次成功；同操作重放返回原修订，参数变化冲突；其他用户/旧 attempt/pausing 写入拒绝；注入约束失败时修订与回执都不提交。
- [x] 加载测试库配置后运行 `go test ./tests/integration -run TestTravelTransactions -count=1`。Expected: 缺少差旅表/Repository 行为失败；缺 TEST_DATABASE_URL 必须失败，不能 skip 冒充通过。
- [x] 实现 design D7 逻辑表与复合归属约束。按会话→Run→业务资源加锁；外部 IO 不进入事务；决策/回执唯一键不含变化 attempt，写入却必须验证当前 attempt/status。
- [x] 重跑该用例，并运行现有迁移/请求幂等/事务回滚相关测试。Expected: PASS，测试只写随机 schema，003 前表和索引保持。
- [x] 自审并提交 `feat: persist travel revisions decisions and operation receipts`；不得因此对开发库擅自 down/清空。

### Task 4: 查询证据与外部 Client

**OpenSpec:** 5.1–5.3；travel-evidence；B05–B06。

**Files:** 扩展 service/trip/query.go、ports.go；创建 client/{train,hotel,weather,search}/client.go、相关配置字段和少量协议测试；扩展 postgres/travel_evidence.go。

**Interfaces:** `QueryClient.Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence,error)`；`Service.Query(ctx,ref,q)` 负责范围内去重、限时、落库与回执引用；Provider 不接收可覆盖认证身份的模型参数。查询结果在保存时再次检查 Run 和绑定修订。

- [ ] 先写 `TestTravelQueries` 的关键边界：独立与自动查询等价去重、过期刷新、跨范围不复用、迟到结果不覆盖、供应方超时/非法格式、酒店价格保持未知。协议测试用 httptest，不访问付费 API。
- [ ] 运行 `go test ./internal/service/trip ./internal/client/train ./internal/client/hotel ./internal/client/weather ./internal/client/search -run 'TestTravelQueries|TestProviderContract' -count=1`。Expected: 新能力缺失或协议断言失败。
- [ ] 根据已核验协议实现聚合车次、高德地点和天气；网页检索仅在可用供应方明确后实现，不复制 Python DDGS 依赖。缓存键含允许数据范围，查询并发有限，可重试只读瞬态错误且有界。
- [ ] 重跑受影响包。Expected: 协议及 Service 测试 PASS；在用户配置可用凭据后各做代表性真实查询并记录，缺配置的真实子项仍未完成。
- [ ] 自审并提交 `feat: add scoped travel query evidence and provider adapters`。

### Task 5: 偏好修订与上下文来源隔离

**OpenSpec:** 7.1–7.3；travel-memory；B12–B14。

**Files:** 创建 model/travel_preference.go、service/travelpreference/{service,ports}.go、agent/memory/{travel,summary}.go；扩展 postgres/travel_preference.go 和 integration/travel_test.go。

**Interfaces:** `PreferenceService.Apply(ctx,ref,operationID,cmd model.TravelPreferenceCommand) (model.TravelPreference,error)`；`ReadEffective(ctx,scope,currentRunID)` 仅返回允许来源修订；`TravelContext.Build(ctx,scope,conversationID,runID,messages []model.Message) ([]model.Message,error)` 在统一预算内注入与压缩，不改变 General 策略。

- [ ] 写 `TestTravelMemoryIsolation` 综合数据库与实际构建输入：偏好保存/撤回、旧 Run 替代后剔除及回退有效版本、混合摘要失效、推断不能覆盖明确值；复用现有 Context 测试补一个超预算配对场景。
- [ ] 运行 `go test ./tests/integration -run TestTravelMemoryIsolation -count=1` 及 `go test ./internal/agent/memory -count=1`。Expected: 缺少来源过滤导致失败，须实际断言唯一旧标记不在模型输入中。
- [ ] 实现带来源修订和摘要来源集合，先过滤再压缩；只分析有效历史，以低频变更版本触发分析，撤回记录不能被推断覆盖。偏好写入复用 Task 3 事务回执。
- [ ] 重跑对应检查。Expected: PASS，更早有效历史仍能使用，工具调用与结果完整配对，不读取 Python 数据。
- [ ] 自审并提交 `feat: isolate travel preferences and summaries by source run`。

### Task 6: 差旅工具、阶段编排和持久化恢复贯通

**OpenSpec:** 6.1–6.4、2.2；travel-execution、travel-planning 事实输出；B08–B12。

**Files:** 创建 agenttools/travel/{read_tool,query_tool,trip_tool,policy_tool,preference_tool}.go；完善 agent/agents/travel/{adapter,agent,runner,state,prompt,checkpoint_store}.go；扩展 Task 1 recovery_test.go，复用 integration/travel_test.go。

**Interfaces:** `NewAgent(cfg Config) (*Agent,error)` 实现 contracts.Agent，Config 注入模型、Trip/Preference Service、Context、DecisionStore 与 CheckpointStore；NewExecution/Start/Pause/Cancel 保持平台既有契约。Tools 使用 read_travel、query_travel、write_trip、query_policy、write_travel_preference，OperationID 从已接受决策绑定，模型不能指定可信执行范围。

- [ ] 先完善 `TestTravelWorkflow`：脚本模型完成规划→查询→校验→选择，断言最终事实来自保存结果；通过同组恢复 fixture 验证提交后断点前退出不重复写入、未知写入拒绝重放、迭代限制恢复不重置。
- [ ] 运行 `go test ./internal/agent/agents/travel ./internal/agenttools/travel -count=1`。Expected: 未实现工具/阶段/回执对应失败。
- [ ] 实现 Handlers 的阶段提示、工具授权与决策包装：完整模型响应在工具执行前持久化，恢复返回已接受决策及原调用 ID；只对实际已提交结果生成完成事实。业务错误形成可修正观察，基础设施未知状态不得自动再写。
- [ ] 完成跨实例真实数据库恢复场景和 `NewExecution` 启动探测零副作用断言。Expected: 新工具/工作流及受影响平台生命周期检查 PASS，探针成为正式用例，不另建一套恢复测试。
- [ ] 自审并提交 `feat: execute resumable travel workflows with business tools`。

### Task 7: 现有 RAG MCP 的政策能力

**OpenSpec:** 5.4–5.5；travel-policy；B07。

**Files:** 创建 service/trip/policy.go、client/mcp/client.go、client/mcp/policy.go；完善 policy_tool.go；增加配置及 .env.example（不改用户 .env）。

**Interfaces:** `PolicyRetriever.Search(ctx,q string) (model.PolicyEvidence,error)` 是内部业务边界；PolicyEvidence 包含状态、来源/片段、适用信息与缺口。client/mcp 将实际 MCP 工具契约映射到该边界；不把旧 stdio/schema_version 写死为未经核验的新协议。

- [ ] 先写少量 `TestPolicyEvidence`，覆盖命中、未命中、超时、不适用、格式错误及检索内容不能授予额外工具权限；内部接口测试不依赖真实服务。
- [ ] 运行 `go test ./internal/service/trip -run TestPolicyEvidence -count=1`。Expected: 缺失政策判断导致失败。
- [ ] 实现内部判断；待用户提供服务地址/位置后检查 transport、工具列表、鉴权及证据格式，再使用实际 MCP 客户端完成连接，协议测试复用同组结果 fixture。无需再建 RAG。
- [ ] 运行受影响检查并实际调用该 MCP 服务。Expected: 内部与协议测试 PASS，真实检索有来源；服务信息未齐时先继续 Task 8 的其他部分，本项真实接入不勾选、不宣称全部重构完成。
- [ ] 自审并提交 `feat: integrate existing RAG MCP policy evidence`，只提交实际完成的部分。

### Task 8: 双入口、阶段事件和前端贯通

**OpenSpec:** 8.1–8.3；identity-and-conversations delta；B11、B15。

**Files:** 修改 bootstrap/bootstrap.go、config/config.go、application/chat/service.go、handler/api.go、agent/manager 的事件处理、frontend/src/{App,api/client}.tsx/ts；扩展现有集成与 App 测试，不复制身份/SSE 测试。

**Interfaces:** 聊天 Application 的可用性按 Agent 类型查询；共享 Submit/Run/Pause/Resume API 保持兼容；新增受限 travel.phase 事件，payload 仅含阶段、关联业务 ID/修订和缺口摘要，不含提示词、凭据或原始完整供应方响应。

- [ ] 先扩展现有 `TestChat`/Agent 列表与 App 用例：Travel 就绪发送、缺配置拒绝、General 与 Travel 分别可用、切换新会话及返回历史。选择精确现有测试名时记录到任务 brief，不另建整个前端套件。
- [ ] 运行 `go test ./tests/integration -run 'Test.*Chat|Test.*Agent' -count=1` 和前端 `npm run test -- --run src/App.test.tsx`。Expected: travel 硬编码禁用导致新断言失败。
- [ ] 注册 Travel，移除硬编码拒绝，保留真实依赖未就绪原因；在 Manager 明确处理和校验新事件，前端展示阶段及真实持久化结果，保持两入口独立。
- [ ] 重跑受影响检查并用浏览器验收差旅提交、页面内切换、关闭、重开手动继续和新问题替代。Expected: 前后端与数据库状态一致，未就绪情况不调用 General 代执行。
- [ ] 自审并提交 `feat: enable independent travel conversations and progress`。

### Task 9: 整体验证、交付与归档

**OpenSpec:** 9.1–9.5；全部规格与 B01–B15。

**Files:** 更新 README.md、docs/{verification,runbook,spec-verification,code-review}.md、配置模板、任务状态与实施记录；CI 沿用现有工作流，只在确有必要时调整。

**Interfaces:** 无新增产品接口；逐项引用实际提交、测试与真实依赖证据，多个场景允许对应同一个关键用例。

- [ ] 自审全部任务与规格，记录已完成实现和未接通的真实依赖。Expected: 能力有对应代码与验证，不以勾选框代替证据，不以测试数量判断完成。
- [ ] 运行 `scripts/verify.ps1 -SkipNpmInstall` 所覆盖的 Go/真实数据库/vet/build/前端检查，并运行 Linux CI race。Expected: 必需检查均 PASS、数据库不 skip；失败修复后只在相关变更及最终确认确有需要时重跑。
- [ ] 少量真实模型、供应方和 MCP 冒烟，加浏览器关键路径验收。Expected: 真实服务与持久化结果可追溯，未提供服务不伪装通过。
- [ ] 单代理整体代码自审及人工规格一致性核对；当前 CLI 无 verify 工作流，明确记录。Expected: 没有重要规格偏差、失败检查或未完项。
- [ ] 只有上述条件满足才 OpenSpec archive、确认归档纳入分支，再按用户选择收尾。Expected: 不把等待服务接入或仅完成核心阶段的状态描述为全部完成。

## 计划自检与执行状态

### 用户补充：演示依赖（对应 OpenSpec 5.6、design D12）

在 Task 4/7/8 的现有接口内增加 demo 查询供应方和演示政策标识。涉及 model/travel_evidence.go、model/travel_policy.go、client/demo、config/bootstrap、查询缓存命名空间、TravelResult 和事实渲染。先用现有 Config/渲染边界证明非法模式、丢失演示标识的失败，再实现；复用真实数据库 fixture 验证标识持久化和模式隔离。编写 docs/fixtures/demo-travel-policy.txt（现有 ingestion CLI 只接收 TXT/PDF），导入 agent_platform_demo，真实 MCP 验证非空片段和文档来源。验证受影响 Go 包及前端用例，最终统一回归。该补充不新增业务能力或另建 RAG 服务。

- 所有 OpenSpec 实施组已映射至 Task 1–9；源码矩阵允许多对一覆盖，不生成一套与正式 specs 竞争的规格。
- 前置顺序：Task 1 探针后实现 Task 2/3，Task 4/5 使用同一业务类型与事务，Task 6 贯通，Task 7 的内部政策接口可先定义供 Task 6 消费；外部服务信息不足不阻塞纯业务实现。
- Task 2 的 Service 依赖消费者 Store，Task 3 Repository 实现它；Task 6 依赖 Service，不 import PostgreSQL。共享 Eino 适配位于 agent 下，不进入 model/Service。
- 执行方式已确定为单代理；2026-10-23 用户明确答复“按计划开始实施（推荐）”。已读取 apply 指导，启动实施并维护本计划执行记录与 OpenSpec tasks。

当前补充证据：TestTravelWorkflowVariants 合并验证往返、活动修正不重复查询、供应方缺失的部分结果；TestTravelDemoQueries 验证演示候选保存及模式缓存隔离。完整本地 verify、Go MCP 演示政策命中已通过。最终浏览器、当前提交 Linux CI 和归档尚未完成，不能整体标记交付完成。
