# 原差旅助手保真迁移 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. 用户已选择单代理，当前代理连续实施并整体自审，不派实施或评审子代理。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在当前 Go 平台分层中恢复原 Python 差旅助手的架构、设计、完整提示词、工具契约和校正闭环，以实际业务结果及模型测量证明迁移。

**Architecture:** Travel 保留在 agent/agents/travel，与 General 并列。Eino 内核保留原版 Main/Verifier/Summary 及辅助角色，工具边界接入共享 Service，Repository 实现所有持久化；平台 Run 与原版执行状态、行程业务状态分开。

**Tech Stack:** 沿用 backend 当前 Go/Eino/pgx 和现有 PostgreSQL、React，不升级依赖，不增加进程或 Go module。

**Spec:** `openspec/changes/preserve-travel-source-flow/specs/`、`design.md`、`tasks.md`；原文来源 `source-baseline.json`，覆盖追踪 `source-mapping.md`，复核 `technical-review.md`。本计划只展开实施，不改变这些文档的行为。

## Global Constraints

- 用户原话：“不要改变原先的架构，设计，提示词”。保留完整原版决策和校正，不以调提示或加预算替代源等价迁移。
- 沿用当前目录和工作分支 `feat/agent-platform-mvp`，用户已要求在本项目内迁移和测试，不新建工作树/仓库，不修改 Python 源项目。
- Service 不依赖 Agent/Eino；工具不绕过 Service 做业务写入；执行/断点经现有消费者存储接口与 Repository。
- 不做 General→Travel 委派、不导入旧 Python 用户数据、不复制上千个测试、不运行 Python 真实模型对照。
- 保留平台页面生命周期、身份、attempt 围栏、幂等、任务替代隔离。旧工作流断点不得强行装入新协议。
- 原版规划批次/工具/外部查询默认分别为 8/8/12；原版业务超时与宿主现有限制分别记录，见 design D8，不能偷偷改 General 上限。
- 测试沿用真实 PostgreSQL 随机 schema；工具用用户允许的固定模拟数据，真实模型配置只从本地 .env 载入，不打印密钥。
- 未完成、失败或未执行的检查不得标为通过；OpenSpec CLI 无 verify 命令，最终做明确人工规格核对。

## Review Focus

1. 一批中第二项参数非法，第一项绝不能已经产生副作用；Task 3 测量 handler 次数为零。
2. 活动文字回复“已改”但存储仍是旧时间；Task 2/6 断言保存修订和活动字段，不以无 error 判成功。
3. 同一 review 只有回执/解释变化导致循环；Task 4 验证源语义指纹及 unchanged_reviews>=2。
4. 业务已提交但断点尚未保存，恢复重放写入；Task 5 注入该窗口，断言修订仅增加一次。
5. 政策/偏好/压缩有模型调用却漏入统计，或被替代任务通过摘要泄露；Task 4/5/6 分别断言模型输入、来源与完整分阶段统计。

## 工作约定

从项目根执行 `. ./scripts/load-env.ps1` 载入配置，进入 backend 后运行 Go 命令，输出保存 `.cache/reports/` 后阅读退出码与相关内容。数据库用例设置 `REQUIRE_DATABASE_TESTS=1`，缺数据库不得以跳过冒充通过。先观察有意义的 RED，再最小实现到 GREEN，整理后只跑受影响检查。最终再统一完整回归。

每项完成更新 OpenSpec tasks 和 `docs/travel-source-fidelity-execution.md`；计划私有 ledger 可位于 `.superpowers/sdd/2026-10-24-preserve-travel-source-flow/`。现有技能脚本如不支持 Windows，采用相同语义记录任务起点提交、测试输出、决定和完成证据，不为运行辅助脚本另建环境。任务提交只包含对应源码、测试和文档，不包含 .env、缓存或 Python 用户文件。

## 文件与接口边界

沿用已有目录，以同包拆分职责，不新建顶层子系统。以下是本次实现接口，不是新增产品 API；如果现有类型可直接复用则保留其名，变更签名须同步 design/计划并记录。

- `agent/agents/travel/prompt.go` 与 `prompts/*.txt`：原文和角色选择。
- `agent/agents/travel/{agent,state,flow,budget,review,reply}.go`：Eino 组装、原版执行状态与决策、预算、审查输入、绑定输出。
- `agent/agents/travel/{policy_agent,preference_agent}.go`：原版辅助角色，复用相同模型客户端和统计包装。
- `agenttools/travel/{tools,contracts,registry,normalize,batch}.go`：完整模型协议、授权 profile、规范化和批次门禁，不承载业务校验。
- `service/trip/{service,constraints,validation,query,policy,reply}.go` 及按职责需要的 `conditions.go`、`patches.go`：确定性业务行为。
- `model/{trip,travel_evidence,travel_policy,travel_execution}.go`：扩充源契约所需的数据；采用现有实际文件，模型层不引入 Eino。
- `agent/memory/{travel,summary}.go`：来源受限上下文、偏好/摘要维护入口；原文动态提示归属差旅能力，通用 General 行为不改。
- `repository/postgres/{trip,travel_execution,travel_evidence,travel_preference,travel_summary}.go`、`agent/ports/stores.go`：必要的执行快照和业务持久化扩展。

工具协议类型 `NativeTripArguments`、`NativeReadArguments`、`NativeQueryArguments`、`NativeMemoryArguments`、`NativePolicyArguments`、`ReplanArguments` 放在 agenttools/travel/contracts.go，字段逐项来自源 registry/trip_contracts/contracts，不使用 map[string]any 随意透传业务参数。业务层只消费 model 类型。

跨任务工程接口：

- 延续 `trip.Service.Apply(ctx, ref model.TravelExecution, operationID string, cmd model.TripCommand) (model.TripResult,error)`、`Inspect(ctx,ref,planID)`，扩充内部业务类型，不破坏权限/回执路径。
- `trip.BindReply(result model.TripResult, spec model.TripReplySpec) (model.TripBoundReply,error)`：只读绑定实际提交结果；ReplySpec 对应源 tasks/explanation，BoundReply 带实际 PlanID/Revision、文本和完成/部分证据。
- `traveltools.ProfileRequest`：Role string、PolicyObserved bool、AllowedTripIDs []string、RetryQueries []model.TravelQuery；生成与源相同的模型可见和服务端授权 profile。
- `traveltools.NativeCall`：ID、Name、Arguments(json.RawMessage)；`PreparedBatch` 保存已规范参数、逐项身份与外部查询键；它不是已成功的业务回执。
- `traveltools.Registry.Profile(ProfileRequest) ([]*schema.ToolInfo,error)`；`Registry.Preflight(ctx context.Context, request ProfileRequest, calls []NativeCall) (PreparedBatch,error)`；预检只读，不生成业务副作用。
- 延续 `travel.NewKernel(ctx, KernelConfig) (adk.ResumableAgent,error)` 和 `travel.NewAgent(Config)`；Config 仅注入消费方接口，不注入具体 postgres。
- `travel.ExecutionState`：原版执行 phase/role、review fingerprint、unchanged count、blocked evidence、errors、stop reason、预算与待查信息，和现有平台 Run 分开。
- `travel.DecideRole(state ExecutionState, review model.TravelReview, results []model.TravelToolResult) string`；`BuildReview(ctx,ref,state)` 通过注入的只读能力组装 model.TravelReview，保留源 review 字段与日历语义。
- 现有 `Policy.Check(ctx, model.PolicyRequest)`、Preferences.Apply 接口继续可被工具调用；其内部辅助模型角色与确定性规则分开注入。

### Task 1: 原文与模型契约基线

**OpenSpec:** 1.1；travel-source-fidelity 的原版提示与工具契约保真；F01/F03。

**Files:** 修改 `agent/agents/travel/prompt.go`；创建 `prompts/{main,review,summary,reply,rail}.txt`、`agenttools/travel/contracts.go`；创建 `agent/agents/travel/source_fidelity_test.go`、`agenttools/travel/testdata/source_contracts.json`。冻结辅助动态提示模板及固定输入输出于相应 testdata；更新本变更 source-mapping，不复制运行中的 Python 配置。

**Interfaces:** 提供原版五个提示值及 Native* 模型参数类型，后续 Task 3 的 Registry 消费这些类型；固定资源支持无 Python 环境的 Go CI。

- [x] 写 `TestTravelSourcePrompts`，完整比对五个原文 SHA256（包括 Main 内 rail 的拼接），并比对 TRIP_ARGUMENT_HELP、工具说明和各 profile 的固定 schema 快照；现有缩写指令应失败。
- [x] 运行 `go test ./internal/agent/agents/travel ./internal/agenttools/travel -run 'TestTravelSource(Prompts|Contracts)' -count=1`；Expected: RED，具体记录缩写提示或缺少原版契约，不以空测试/跳过证明缺失。
- [x] 从冻结源逐字移入静态原文；定义源等价 Native 类型、默认/互斥/空值语义，输出固定 schema。政策/偏好/压缩提示用固定输入冻结构成，不自行改写，也不在本任务调用外部模型。
- [x] 重跑该命令；Expected: GREEN。审查只有原文资源/契约及其有意义测试，不因这一任务声称链路已恢复。
- [x] 提交 `refactor: freeze original travel prompts and model contracts`，记录 SHA256 与源哈希一致，更新 1.1 的已完成范围。

### Task 2: 原版业务语义与绑定回复

**OpenSpec:** 2.1/2.2；travel-planning；F06–F09/F16。

**Files:** 修改现有 model/trip.go、travel_evidence.go、service/trip/{service,validation,constraints,query}.go、client/train/client.go；新增 `service/trip/{conditions,patches,reply}.go`、`service/trip/source_rules_test.go`。源字段若可在现有快照 JSON 中表达则复用，不无故追加表。

**Interfaces:** 扩充 model.TripCommand/TripPlan/TripResult 保留源 conditions/source/strength、task revision、route_tasks/components/content、allow_repair 与源选择语义；提供 model.TripReplySpec/TripBoundReply 和上文 BindReply；Inspect 返回足够组装 review 的真实快照。模型参数到业务命令的转换留 Task 3。

- [ ] 写综合 `TestTripSourceRules`：默认往返/明确单程；多城与逐晚住宿；high_speed 与 G 规范匹配；元/分精确及未知；粗时段不捏造；只改活动保留候选且查询数零；日期改动只失效依赖；完整路线修改；硬条件不可降级。按源函数/测试语料提取代表性输入，合并表驱动，不镜像实现。
- [ ] 写 `TestTripBoundReply`：本次活动 14:00→16:00 后回复来自新修订；未提交/旧回执不能证明新修改；局部 content 展示与整路线完整展示；部分结果不冒充全部满足。
- [ ] 运行 `go test ./internal/service/trip -run 'TestTrip(SourceRules|BoundReply)' -count=1`；Expected: RED，观察实际缺失业务断言。
- [ ] 按源 contracts/conditions/strengths/activities/patches/dependencies/validation/constraints 和 trip_reply 逐项实现，保持当前 Apply 的权限、回执和事务契约，返回可定位字段的源语义错误；车种在供应方边界规范，业务层用规范值。
- [ ] 运行 `go test ./internal/service/trip ./internal/client/train -count=1`；Expected: GREEN，Service 没有 Agent/Eino 依赖；更新 source-mapping 对应证据。
- [ ] 提交 `fix: preserve original trip rules patches and bound replies`，完成 2.1/2.2。

### Task 3: 工具协议、批次门禁与自动查询

**OpenSpec:** 3.1/3.2；travel-execution 批次预检；F03–F05/F15/F18。

**Files:** 拆分现有 agenttools/travel/tools.go 为同包 `contracts.go`、`registry.go`、`normalize.go`、`batch.go` 和必要业务 handler 文件；修改 Travel `agent.go`、`journal.go`、client/providerhttp/http.go，同包添加 `resilience.go` 与 `resilience_test.go`；创建 `agenttools/travel/source_batch_test.go`、`agent/agents/travel/batch_gate_test.go`，复用当前工具 fixture。

**Interfaces:** 提供 Registry.Profile/Preflight、ProfileRequest、NativeCall/PreparedBatch，保留现有 Config/工具初始化调用兼容到 Task 4 接线。Task 2 的业务函数是唯一业务写入口。

- [ ] 写 `TestTravelSourceBatch` 表驱动：整批第二项非法→handler 总次数零；同计划两写拒绝；不同计划合法独立写允许；政策与受影响规划同批拒绝；偏好与规划同批允许；replan 只许单独调用；review 禁止 propose/偏好，独立重试只许原失败条件；规范错误带字段而非统一候选过期。
- [ ] 写 `TestTravelEinoBatchGate`，真实 Eino＋脚本模型＋计数工具；证明模型后整批预检先于并发工具执行，拒绝结果与每个 call_id 配对，下一轮模型能看见原因；实际 bound reply 能在工具后结束而不再调用模型。
- [ ] 写 `TestTravelProviderResilience`，注入时钟/RNG/Doer，覆盖源重试错误集合、最多 extra_retries=2、Retry-After 不越剩余预算、60 秒窗口内 5 次失败开路、60 秒冷却后单个半开探测及 2 次成功恢复、取消释放，拒绝参数不重试；不使用真实 sleep 或付费 API。
- [ ] 运行 `go test ./internal/agenttools/travel ./internal/agent/agents/travel -run 'TestTravel(SourceBatch|EinoBatchGate)' -count=1`；Expected: RED，现有单项执行和扁平协议无法通过。
- [ ] 恢复 read/query/write_memory/write_trip/query_policy/request_replan 与源 profile；模型后建立批次门禁，工具包装共同消费预检结果，禁止 Eino 默认并行抢先副作用；保持 journal 模型决策、逐项操作 ID 和回执完整。源 normalize 只转换无歧义表示，不推断硬约束。
- [ ] 恢复查询地图待查作业、等价缓存、真实依赖与有界并发；拒绝与运行失败分别记录。单批多个计划不是跨计划原子事务，每项实际结果必须独立可恢复。
- [ ] 在 Client 边界实现源端点级熔断和单一重试负责人，工具不额外叠加重试；沿用原参数和取消语义，不改变凭据保护。先运行 `go test ./internal/client/providerhttp -run TestTravelProviderResilience -count=1` 观察 RED，完成实现后同命令 GREEN。
- [ ] 运行上述新用例及 `go test ./internal/agenttools/... ./internal/agent/agents/travel ./internal/client/providerhttp -count=1`；Expected: GREEN。若 Eino 门禁探针失败，先定位实际回调顺序并更新 design 的等价接线，不绕过源预检。
- [ ] 提交 `fix: restore native travel tools and batch preflight`，完成 3.1/3.2。

### Task 4: 原版角色、上下文和校正闭环

**OpenSpec:** 4.1/4.2；travel-source-fidelity 全部决策和停止场景；F01/F02/F11–F16/F19。

**Files:** 修改 Travel `{agent,state,adapter}.go`；同包新增 `{flow,budget,review,reply,policy_agent,preference_agent}.go`；修改 agent/memory/{travel,summary}.go、service/trip/policy.go、service/travelpreference/service.go、bootstrap/bootstrap.go、config/config.go；增加 `travel/source_flow_test.go`，在既有 memory/policy 测试中扩展表项。

**Interfaces:** ExecutionState/DecideRole/BuildReview 消费 Task 2 真实业务快照和 Task 3 Registry；角色动态 ToolInfos 与服务端相同授权。Policy/Preference 辅助角色消费现有模型接口与只读检索/历史接口，确定性验证消费 model 类型，不把模型调用放进 Service/Repository。状态序列化保留业务批次、活跃时间、错误/blocked evidence、review 指纹、重试、交接及待查。

- [ ] 写一个综合 `TestTravelSourceFlow`：Main→propose/自动查询→Verifier→局部 amend/select＋绑定收尾；全局遗漏→独立 replan→Main 修现有行程；纯查询→Summary 不写计划；政策观察后回 Main；多个计划同时进入 review。
- [ ] 同一表中增加：语义 review 三次相同但回执/文字变化→unchanged_reviews=2 停止；真实修改重置计数；允许 partial 同轮停止；无有效修改不能判成功；预算计数在开始规划后的批次累计且拒绝批次按原版计数，不使用两次 propose/三次 amend 限制。
- [ ] 为政策/偏好/压缩复用脚本模型固定输入：完整原文与 contract golden；有证据政策仅一次提取且核验引用；资料不足保留缺口；偏好只分析符合源要求的新确认有效行程，模拟/失败行程不参与；压缩仅完整旧轮次、至多三个前缀、80% 触发，工具配对和来源不丢。
- [ ] 运行 `go test ./internal/agent/agents/travel ./internal/agent/memory ./internal/service/trip ./internal/service/travelpreference -run 'TestTravelSourceFlow|TestTravelSourceAuxiliary|TestTravelContext' -count=1`；Expected: RED，缺角色/审查/辅助行为导致断言失败。
- [ ] 对照源 loop/state/limits/registry/planning 全量分支接回 Eino，原文与 schema 使用 Task 1 资源，移除缩写指令和额外修改次数限制；注入原版 review/calendar/候选视图及 execution facts。角色切换不经过 AgentManager，不新增独立决策循环。
- [ ] 实现源等价政策提取/核验、偏好触发、压缩及恢复信息；保留源默认业务限制与明确宿主围栏，配置与停止原因不能混淆。建立所有辅助模型调用的统一阶段标记供 Task 6 统计。
- [ ] 运行受影响 Agent/Memory/Service/Config 单测；Expected: GREEN，General 的模型输入与默认限制保持现有行为。
- [ ] 提交 `fix: restore original travel review correction and stopping flow`，完成 4.1/4.2。

### Task 5: 真实事务、断点与平台隔离

**OpenSpec:** 5.1/5.2；现行 travel-execution 安全/幂等/隔离要求；F04/F05/F12/F14/F17/F19。

**Files:** 修改 agent/ports/stores.go、Travel `{journal,state,adapter}.go`、postgres 的 travel_execution/trip/travel_evidence/travel_preference/travel_summary；扩展 `backend/tests/integration/travel_workflow_test.go`、`travel_memory_test.go`，必要时新建 `travel_source_recovery_test.go`。确需新表/列时只追加下一序号 migration 并修改迁移注册，不重写已有 migration。

**Interfaces:** 延续平台 Agent contracts 与现有 ports 方法；新执行快照包含 Task 4 所有字段及 Task 3 批次/作业状态。workflow fingerprint 纳入原文、schema、flow version；旧指纹拒绝恢复但历史可查。

- [ ] 写 `TestTravelSourceRecovery`：同批不同计划第一项已提交、第二项未执行时中断；作业查询已返回但后续断点未保存；重建 Agent/Runner 仅用库中数据恢复，验证提交只发生一次、剩余项正确续跑、角色/预算/无进展数不清零。
- [ ] 扩展现有隔离场景：旧 attempt 迟到拒写；旧断点不兼容时无模型/外部查询/业务写；替代任务的偏好和混合摘要不进入实际模型输入；页面内切换继续、关闭安全暂停、打开不自动执行。
- [ ] 在数据库隔离 schema 运行 `go test ./tests/integration -run 'TestTravel(SourceRecovery|WorkflowRecovery|MemoryIsolation)' -count=1`，并设置 REQUIRE_DATABASE_TESTS=1；Expected: RED，由新快照或恢复语义缺失导致，不是数据库配置错误。
- [ ] 完成快照/作业/回执存储适配，复用原事务围栏和成功回执，不把 prepare/accepted 当 completed；未知副作用继续不可自动重放。保留启动兼容性检查无副作用。
- [ ] 重跑上述命令及相关平台 page/run 恢复用例；Expected: GREEN 且没有 SKIP。记录数据库迁移及新旧数据兼容结果。
- [ ] 提交 `fix: persist original travel flow across safe recovery`，完成 5.1/5.2。

### Task 6: 真实模型结果与性能验收

**OpenSpec:** 6.1/6.2；迁移证据与测量口径；F10/F11/F18。

**Files:** 将 `.cache/performance/travel_perf_probe_test.go` 的测量逻辑整理到 `backend/tests/integration/travel_source_model_test.go`，显式 opt-in 环境开关；复用 `travelFixture`，不新增第二套数据库框架。修改既有模型包装的必要统计边界；记录 `docs/travel-source-fidelity-verification.md` 和脱敏报告，原始大日志保留 .cache。

**Interfaces:** 实际所有模型调用经带 Stage 的计量包装；记录逻辑调用次数、起止/耗时、输入/输出 Token 与 Usage 是否提供；计数工具和实际数据库结果独立于模型文本。缓存命中是否调用模型按实际记录，SDK 内部 HTTP 重试不冒称独立模型决策。

- [ ] 先写可离线运行的 `TestTravelMetricsAccounting`：Main/Verifier/政策/偏好/压缩各阶段相加等于总数；Usage 缺失标记 unknown，不记为零；失败调用同样计数。运行到 RED，再补最小计量包装，重跑 GREEN。
- [ ] 用真实模型、固定模拟工具、真实随机 schema 运行查询、单程、多城、局部活动修改各 3 次；使用修复前保存的同组请求和判据，严禁减少旅段或排除活动让测试变绿。执行 `go test ./tests/integration -run TestTravelSourceModel -count=1 -timeout 30m`，设置 REQUIRE_DATABASE_TESTS=1、REQUIRE_TRAVEL_SOURCE_MODEL_TEST=1；Expected: 12 份有完整结果的报告，所有业务断言通过才可通过此任务。
- [ ] 分场景报告每次结果、成功率、输入/输出/总 Token、各阶段调用数、耗时范围、工具调用和实际完成字段；测量从 Go Agent 执行到结果持久化返回，不含 HTTP/SSE/UI，非流式不报告 TTFT。失败同样保留，不用多次重跑挑最好结果。
- [ ] 若失败，读角色/规范参数/实际观察和存储差异，先定位对应源行为，再补一项有意义的 RED→GREEN 回归修复；规格或设计需变更时先同步文档，不擅自改提示词。修复后重跑受影响场景并保留前后结果，最终全组一次验收。
- [ ] 运行 `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/server`；frontend 运行 `npm test -- --run` 和 `npm run build`。Linux race 沿用已有 CI；未执行不得报告通过。Expected: 所有必要检查 GREEN，失败未解决则本任务保持未完成。
- [ ] 自审原版行为映射与 Go import 方向，提交 `test: verify source faithful travel workflow and model metrics`，完成 6.1/6.2。

### Task 7: 一致性核对与交付

**OpenSpec:** 7.1/7.2。

**Files:** 更新 README、docs/travel-source-fidelity-execution.md、docs/travel-source-fidelity-verification.md、source-mapping 及 OpenSpec tasks；只有实际完成后才归档本 change。

- [ ] 按每个 Requirement/Scenario、F01–F19、任务及详细计划核对源码和测试证据，检查没有只有勾选框的完成项；记录整体自审发现及修复。
- [ ] 运行 `openspec validate preserve-travel-source-flow --strict`；Expected: PASS。单独记录人工一致性核对，不称执行过不存在的 verify。
- [ ] 更新 README：原版已保留内容、实际测试结果、模拟工具标识、真实依赖与剩余缺口。失败/未做必须直说，不能沿用旧“迁移完成”结论掩盖偏差。
- [ ] 仅在任务、测试、审查、一致性满足后使用 OpenSpec archive 同步规格；复核 archive 内容和 diff，提交归档文档。
- [ ] 按分支收尾流程报告已验证结果与未做项，呈现适用的提交/PR/保留分支选择并执行用户授权，不自动合并共享分支。

## Pre-flight 与自审结果

- Task 1 的提示及 Native 类型由 Task 3 Registry 消费；Task 4 只选角色原文，不另写指令。
- Task 2 的 Apply/Inspect/BindReply 被 Task 3/4 调用；模型 DTO 不渗入 Service，业务 DTO 不直接替代模型 schema。
- Task 3 的全批门禁和操作身份被 Task 5 回执恢复消费；稳定 operationID 与变化的 attempt 分开。
- Task 4 的全部执行字段被 Task 5 序列化；平台 Run 不嵌入重复业务状态。
- Task 6 使用 Task 4 已标记的所有模型阶段并核对 Task 2 实际状态，不以文本或 nil error 验收。
- 所有规格场景均映射到以上任务；旧平台身份、暂停、隔离继续使用既有回归。当前仅计划与来源记录完成，以上实施复选框均未勾选。
