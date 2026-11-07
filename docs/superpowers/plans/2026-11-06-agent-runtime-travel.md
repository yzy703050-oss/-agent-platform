# 小豆包 Agent 可靠运行与独立 Travel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 用户选择单代理时使用 superpowers:executing-plans；选择多子代理时使用 superpowers:subagent-driven-development。Steps use checkbox (`- [ ]`) syntax for tracking。用户已选择单代理，不派子代理。

**Goal:** 按原Agent change实现双入口共享查询、可靠专业委派、独立Travel、版本化计划、来源/记忆保护及多实例可恢复交付。

**Architecture:** 沿用同一Go module，新增platformpg/travelpg及受限control访问；平台API、Worker、Travel、Maintenance独立装配。父Job经完整Eino现场＋Outbox联合事务waiting，Kafka只分发专业调用，Dispatch经MCP提交，真实结果再恢复原调用；RAG保持同步。

**Tech Stack:** Go1.26.x支持基线、Eino v0.9.0-beta.1、Go MCP v1.8.0、PG17、Kafka4.4.0/franz-go v1.22.2、Redis8.10.3/go-redis v9.23.0、JCS v1.0.1、现有React/TypeScript/Vitest。

**Spec:** [原specs](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/)、[design](../../../openspec/changes/refactor-agent-kernel-plan-execute/design.md)、[tasks](../../../openspec/changes/refactor-agent-kernel-plan-execute/tasks.md)、[公共协议](../../../openspec/changes/refactor-agent-kernel-plan-execute/engineering-protocol.md)、[MQ/MCP协议](../../../openspec/changes/refactor-agent-kernel-plan-execute/mq-mcp-dispatch-protocol.md)、[数据库/迁移](../../../openspec/changes/refactor-agent-kernel-plan-execute/database/README.md)、[技术复核](../../../openspec/changes/refactor-agent-kernel-plan-execute/engineering-review.md)。本计划只展开执行，不另定义需求。

日期：2026-11-06。状态：单代理实施中；A00已完成代码检查，真实环境2.6待就绪。原仓库为 `G:/简历项目/小豆包`，本次隔离实施目录为 `C:/Users/Administrator/.codex/worktrees/agent-runtime-travel/小豆包`，命令默认在其 `backend` 中运行；标为pwsh/仓库命令的步骤按所示路径运行。RAG实现根为 `G:/简历项目/MODULAR-RAG-MCP-SERVER`，其工作按[配套计划](2026-11-06-rag-mcp-retrieval-service.md)。

## Global Constraints

- Run默认300秒，接纳即固定deadline；排队、waiting、重试和父表达均计时，不续长。
- waiting父清执行租约、停止续租、退出释放槽；ready重认领新epoch。同步RAG期间Travel仍running、续租并占专业槽。
- 最多3次重试/4次总尝试，full jitter U(0,1s)/U(0,2s)/U(0,4s)，恢复不清次数；同逻辑调用只能一个网络重试责任方，网页保留零自动重试。
- Travel最多8个“执行＋重新规划”轮次，连续2轮无实质进展结束；初始Planner不计轮。最终failed/部分结果交父，不自动新Task或换key。
- 同PG实例，platform/travel各自schema和运行角色；只在本服务建业务FK。control私有表只经白名单函数，授权撤回与业务提交按正式锁序裁决。
- 一个用户问题一个Run；waiting仍互斥；纯查询无TripPlan。两入口规划同能力，差旅入口不先调用General。
- 无用户暂停/继续/整轮取消/活动Run补充，页面关闭不停止任务；旧数据库数据按2026-11-07用户决定不保留；新运行仍保护撤回与来源排除。
- 计划首版完整创建、后续受控Patch；Task变化全Task Item重查、无关Task保留；selection不升查询版本，refresh增generation。
- 20,000估算token/80%检查/单次最多3个旧完整轮次；每入口主Thread10轮成功有效对话检查长期记忆，reviewed/summarized分开、空更新允许。
- RAG不是子Agent，不建RAG Job/Topic/Outbox；API Key不代替用户授权。未知报价为null，酒店地点不等于报价/库存/预订。
- 新部署从空库建立新schema，不导入旧数据；001～005代码与冻结报告仅作工程追溯，不覆盖原.env。用户已授权安装Docker所需后端，实际就绪仍须验证，模型缺失不得伪报。

## Review Focus

1. 并行工具产生多个中断时，仅原调用及已满足target恢复；错误target/缺依赖保持waiting。对应A08/A11。
2. Session撤回恰好与Travel提交竞争：只有按guard锁顺序先发生的合法提交可成功，不能只做远端预检。对应A03/A15。
3. 含emoji键、大整数、显式null和重复键的输入不可因错误JCS或有损JSON合并成相同业务身份。对应A01/A07。
4. Redis丢许可、失去owner及仍在途HTTP并存时，不能立即放出正式资源额度或退未知预算。对应A03/A05/A15。
5. 空库初始化及重放时，platform/travel保持隔离，不导入旧任务或依赖public旧表。对应A02/A16。

## 执行顺序、前置条件与文件职责

- 顺序主线：A00→A01→A02→A03→A04；之后按每任务Depends推进。A05共享查询、A06计划、A07Task/MCP完成后，A08父挂起与A09分发闭环；A10专业执行、A11结果恢复、A12记忆、A13进程、A14前端、A15联合故障、A16交付。
- A10消费自己定义的ContextReader端口，先用授权夹具验证，A12实现并在A13接线；这只是接口依赖，不能跳过真实上下文验收。
- RAG R00～R07独立实现；A15消费R07已完成HTTP服务/真实检索。RAG R10消费A15一次联合报告；A16在R10后归档，避免环形依赖。
- 用户已要求Docker部署并授权准备依赖；2026-11-07补全DockerDesktop安装，WSL引擎、六个基础容器及PG/Redis/Kafka基础协议与Milvus健康探针已通过。业务角色/迁移与真实模型尚未验收，A00预检仍是门槛。真实服务可来自显式隔离远端配置。无服务的单元任务可开展；依赖真实服务的任务必须停在未完成，不能SKIP后打勾。
- 第一次执行先按using-git-worktrees检查native隔离/当前分支；两个仓库分别盘点已跟踪/未跟踪WIP，只迁入任务必需且有清单的基线，保留原工作区。若分支复现必须纳入既有改动，先展示具体清单再确认，不能把无关WIP提交成当前任务。
- 新运行模型位于internal/model/runtimev1，协议严格解码在runtimeprotocol；platformpg/travelpg只持所属schema连接；control/permit只管授权/调用账/预准入；service/travelplan管正式业务；toolquery管无计划事实；旧service/trip和repository/postgres仅为尚未清理旧代码的回归基线，新部署不装配它们。
- A02 fixture使用独立临时数据库＋真实登录角色，因为session_user不能用SET ROLE假造；REQUIRE_RUNTIME_TESTS=1时缺管理员/服务必须FAIL。清理只针对本次记录的xdb_v1_测试资源，不指向开发库。
- 每任务遵循红→绿→整理→复测；记录命令、退出码、预期失败根因和实际结果到docs/reports/runtime-v1及原tasks对应行，不另建需求或讨论快照。计划checkbox记录步骤，OpenSpec tasks记录交付状态。
- 每任务第5步仅提交相对起始基线的本任务增量：检查diff并选择hunk，不盲目git add整个已有修改文件；无法安全分离则保留工作区并列出具体冲突，不提交他人改动。单代理由当前代理自审；仅用户选择多子代理时逐项独立规格/质量评审。

## 实施任务

多条命令逐条检查退出码，PowerShell任一非零立即停止；Linux CI每项独立fail-fast step。`$runtimeProfile`为A00生成/核验的 `G:/简历项目/小豆包/.cache/runtime-v1/profile.json`，明确选择本机或隔离远端端点。

### A00：隔离执行环境、有限配置与依赖基线

**OpenSpec mapping:** 实施子项3.1.0；2.5、2.6、3.1；streaming-and-delivery/可复现交付与真实验证。
**Depends:** 计划审阅及执行方式已选择；尚不安装系统服务。

**Files:**
- Create: scripts/tests/test_runtime_tools.py
- Create: docs/reports/runtime-v1/A00.md
- Create: backend/internal/config/runtime.go
- Create: backend/internal/config/runtime_test.go
- Create: deploy/local/runtime-v1.example.json
- Create: deploy/local/compose.runtime-v1.yaml
- Create: scripts/runtime-preflight.ps1
- Create: scripts/verify-runtime.ps1
- Modify: backend/go.mod
- Modify: backend/go.sum
- Modify: .github/workflows/ci.yml

**Interfaces:**
- Consumes: 既有config.Load；原engineering-review第4节固定版本/digest；隔离PG/Kafka/Redis端点。
- Produces: config.LoadRuntime(get func(string) string) (config.RuntimeConfig, error)。RuntimeConfig固定Role、InstanceID、PlatformDSN/TravelDSN/Control连接配置、Run/Tool/Lease/轮询期限、模型与工具有限累计上界、模型输入/输出token上界、每账户资源限额、服务地址。仅加载当前Role所需凭据。scripts/verify-runtime.ps1 -Case <A编号|All> -Profile <隔离JSON路径> -RequireCases；预检输出仅依赖名/版本/状态。

- [x] **Step 1 — 先写有意义的失败测试：** TestRuntimeConfigRequiresFiniteBudgets；TestRuntimeConfigSeparatesRoles；TestRuntimeConfigPreservesAbsoluteLimits。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
// 删除模型/工具尝试及token有限上界后必须拒绝；不能沿用无限预算。
if err == nil { t.Fatal("missing finite budget accepted") }
if cfg.RunSeconds != 300 || cfg.ToolSeconds != 10 { t.Fatal("unexpected defaults") }
if cfg.EntrySlots != 2 || cfg.DispatchSlots != 2 || cfg.TravelSlots != 1 { t.Fatal("pool defaults") }
```
- [x] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/config -run '^TestRuntimeConfig' -count=1
```
预期：FAIL，新Runtime配置入口不存在，或旧配置仍默认Run120秒/未拒绝缺少有限预算；缺Docker不是单元测试的预期失败原因。
- [x] **Step 3 — 最小实现：** 保留既有Load用于legacy；新增Role=api/worker/travel/maintenance/migrate的配置校验，默认Run300秒、工具10秒、续租5秒/租约20秒、PG池4、Outbox轮询1秒/批100/租约30秒、巡检1秒。父返回余量配置为正数且小于Run窗口，隔离测试用15秒（工程起点，不是产品固定值）。fixture预算用32个模型尝试/64个工具尝试、500000 token上界，只用于脚本模型，正式live配置须显式给值。加入franz-go v1.22.2、go-redis/v9 v9.23.0、gowebpki/jcs v1.0.1，保持Eino/MCP不升级，解析完整go.sum；Compose使用复核digest并补齐etcd/MinIO锁。预检可检查已存在的本机容器或显式远端端点，不自动安装Docker/WSL、启动业务服务、拉模型或加载现有.env。
- [x] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：单元通过、go mod verify及当前工程编译通过，实际环境缺项明确非零退出；新增依赖下载/编译后才更新2.5，基础服务/隔离账号真实健康后才更新2.6。环境未就绪时可继续纯单元任务，真实DB任务不得伪通过。
- [x] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A00 隔离执行环境、有限配置与依赖基线`）。

### A01：正式领域DTO、运行协议与严格JCS

**OpenSpec mapping:** 实施子项3.1.1；2.3、3.1；agent-delegation/独立MCP子Agent接入；tripplan-lifecycle全部DTO。
**Depends:** A00依赖解析完成。

**Files:**
- Create: backend/internal/runtimeprotocol/agent-contracts.schema.json（原Schema部署副本，字节一致性测试约束）
- Modify: scripts/verify-runtime.ps1
- Modify: scripts/tests/test_runtime_tools.py
- Create: docs/reports/runtime-v1/A01.md
- Create: backend/internal/model/runtimev1/authorization.go
- Create: backend/internal/model/runtimev1/run.go
- Create: backend/internal/model/runtimev1/wire.go
- Create: backend/internal/model/runtimev1/plan.go
- Create: backend/internal/model/runtimev1/evidence.go
- Create: backend/internal/model/runtimev1/memory.go
- Create: backend/internal/runtimeprotocol/codec.go
- Create: backend/internal/runtimeprotocol/codec_test.go
- Create: backend/internal/runtimeprotocol/testdata/jcs-vectors.json

**Interfaces:**
- Consumes: 原contracts/agent-contracts.schema.json全部适用$defs，尤其TravelToolArgs/PlanDefinition/Patch/Memory及七个运行v1定义；现有model仅作legacy类型。
- Produces: runtimev1中同名Go DTO逐一映射Schema（不含Eino依赖）；LeaseRef{WorkerID string,Epoch int64,ExpiresAt time.Time}；ExecRef{ScopeID,ScopeHash,OwnerKind,OwnerID,ThreadID string;Lease LeaseRef}，OwnerKind=entry_job/travel_task/memory_operation。runtimeprotocol.DecodeStrict(raw []byte, definition string, dst any) error；InputHash(args json.RawMessage, meta runtimev1.TrustedTravelMeta) (string,error)；ValidateResult(receipt runtimev1.TravelTaskReceipt, result runtimev1.TravelTaskResult) error。 另在run.go定义RunView{ID,Status string;DeadlineAt time.Time}、JobLease{Ref ExecRef;ScheduleSeq,CheckpointVersion int64;DeadlineAt time.Time}、TravelTaskLease{TaskID string;Ref ExecRef;CheckpointVersion int64;DeadlineAt time.Time}，供后续本地仓储消费，其他字段仅按正式归属/版本表补充。 LimitSnapshot在run.go定义固定MaxModelAttempts/MaxToolAttempts/TokenUpperBound/MaxModelInputTokens/MaxModelOutputTokens及ToolTimeout/DeadlineAt，取自接纳时配置，不在恢复时重新读取默认值。

- [x] **Step 1 — 先写有意义的失败测试：** TestWireFixtureParity；TestJCSCanonicalInput；TestRejectSpoofedScopeAndAcceptedAsResult。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
// RFC8785对象键按UTF-16排序；非ASCII键、指数、-0和转义使用固定向量。
if hashA != hashB { t.Fatal("key order changed business identity") }
if errDuplicateKey == nil || errUnsafeInteger == nil { t.Fatal("lossy input accepted") }
if errAcceptedResult == nil || errFailedAsSuccess == nil { t.Fatal("receipt mistaken for result") }
```
- [x] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/runtimeprotocol -count=1
```
预期：FAIL，新协议解码/摘要入口不存在，或旧RuntimeScope/普通JSON无法满足JCS向量、重复键及受理/结果分离。
- [x] **Step 3 — 最小实现：** 手写集中且有责任划分的类型，不为旧DTO做全仓无差别替换。DecodeStrict在JCS前拒绝重复键、非法UTF-8/孤立代理、未知字段、多JSON值、不安全整数/非有限数；保留缺省与null差异。用固定jcs.Transform而不是encoding/json排序模拟JCS。InputHash严格采用原协议4.1对象，密钥/transport ID/父epoch不入hash；样例37～43由Go验证，caller仍来自认证，Schema成功不代表授权。
- [x] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：43份原夹具的对应结构、RFC向量及身份/结果负例均通过；领域与运行类型命名区分计划TaskDefinition和专业TravelTask。
- [x] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A01 正式领域DTO、运行协议与严格JCS`）。

### A02：全新三schema初始化、版本迁移与账号隔离

**OpenSpec mapping:** 实施子项3.1.2；2.4、3.1；agent-delegation/全新数据库部署与新请求切换；identity-and-conversations/用户数据隔离。
**Depends:** A00真实隔离PG及管理账号可用；A01。用户于2026-11-07取消旧数据库数据保留。

**Files:**
- Create: backend/cmd/migrate/main.go
- Create: backend/internal/migration/runner.go
- Create: backend/internal/migration/roles.go
- Create: backend/migrations/control/001_coordination.up.sql
- Create: backend/migrations/platform/001_runtime.up.sql
- Create: backend/migrations/travel/001_runtime.up.sql
- Create: backend/internal/repository/platformpg/repository.go
- Create: backend/internal/repository/travelpg/repository.go
- Create: backend/tests/runtimeintegration/fixture_test.go
- Create: backend/tests/runtimeintegration/migration_test.go
- Modify: backend/migrations/migrations.go
- Modify: scripts/verify-runtime.ps1
- Modify: .github/workflows/ci.yml
- Modify: .gitattributes

**Interfaces:**
- migration.Provision(ctx,pool,prefix,passwords)仅由管理员显式准备专用角色；Principals不含密码。
- migration.Apply(ctx,pool,component)仅由对应组件迁移登录执行；control→platform→travel串行、摘要校验、可重放；platformpg.New(pool)、travelpg.New(pool)划定本地仓储。
- newRuntimeFixture(t)创建xdb_v1_专用临时数据库与真实角色；清理必须核对前缀和记录OID。新部署不调用旧001～005或任何legacy导入/读取接口。

- [x] **Step 1 — 先写有意义的失败测试：** TestRuntimeMigrationIdempotent、TestSchemaRolesDenyCrossBusinessAccess、TestRuntimeMigrationConstraints、TestRuntimeMigrationFreshDatabase、TestRuntimeMigrationRejectsChangedHistory。覆盖新结构、独立登录权限、同服务复合归属、空库无旧数据导入与已执行SQL摘要保护。
- [x] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(RuntimeMigration|SchemaRoles)' -count=1 -v
```
预期：目标schema/角色/约束尚未实现而失败；缺隔离PG明确失败，不能SKIP。
- [x] **Step 3 — 最小实现：** 独立NOLOGIN owner/迁移登录/运行登录，固定search_path和最小schema权限；三个服务本地结构、唯一键、FK、租约/状态/计数/断点CHECK及扫描索引；control私有guard/scope/resource_limits/outbound_calls；接收方evidence_imports固定原身份、hash、授权来源和原payload。跨服务不建业务FK，不创建旧数据兼容字段、旧历史读取账号或导入程序。
- [x] **Step 4 — 使用Step 2同一命令复测并整理：** 全新初始化及重复迁移通过；真实session_user证明权限；完整后端测试、vet、模块和构建通过，报告明确可用依赖及跳过项。
完成条件：新库可以初始化和安全重放，运行账号无需DDL权限；没有跨schema业务FK，也没有旧数据库运行状态导入。
- [x] **Step 5 — 审查与记录：** 当前代理自审，维护任务和报告，仅提交本任务增量；建议提交消息`feat(runtime): initialize isolated schemas and migration roles`。

### A03：原子授权撤回、共享预算与Redis准入

**OpenSpec mapping:** 实施子项3.1.3；2.3、3.1、3.3；agent-execution/共享额度和累计调用预算；identity-and-conversations/多实例登录与有效执行授权。
**Depends:** A01、A02；真实Redis供故障集成。

**Files:**
- Create: backend/migrations/control/002_capability_functions.up.sql
- Create: backend/internal/control/client.go
- Create: backend/internal/control/types.go
- Create: backend/internal/permit/redis.go
- Create: backend/internal/permit/scripts/acquire.lua
- Create: backend/internal/permit/scripts/release.lua
- Create: backend/tests/runtimeintegration/authorization_budget_test.go
- Modify: backend/internal/service/auth/service.go
- Create: backend/internal/repository/platformpg/auth.go

**Interfaces:**
- Consumes: A01 ExecRef；A02私有control表与真实登录角色。
- Produces: control.Client.IssueScope(ctx context.Context, tx pgx.Tx, in ScopeInput) (AuthorizedScope,error)；AssertScope(ctx context.Context,tx pgx.Tx,ref runtimev1.ExecRef,capability,target,purpose string) (AuthorizedScope,error)；ReserveAttempt(ctx context.Context,tx pgx.Tx,in AttemptInput) (Attempt,error)；SettleAttempt(ctx context.Context,tx pgx.Tx,in Settlement) error。ScopeInput/AuthorizedScope字段逐一采用公共协议2.1；AttemptInput包含logical_key/attempt_no/owner_epoch/resource_keys及有限Reservation；Settlement包含原attempt_id/真实usage/status/response_ref。permit.Acquire(ctx,resourceKey,attemptID string,until time.Time) error与Release(ctx,resourceKey,attemptID string) error。 control.Reservation定义ModelAttempts/ToolAttempts/TokenUpperBound/CostUpperMicros，均非负有限整数且依原能力上界校验；permit.Service为Acquire(context.Context,string,string,time.Time) error和Release(context.Context,string,string) error接口。

- [ ] **Step 1 — 先写有意义的失败测试：** TestAuthorizationCommitRevocationRace；TestServiceRoleScopeIsolation；TestBudgetReservationSurvivesUnknownUsage；TestRedisFailoverCannotMultiplyPermits。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if committedAfterRevocation { t.Fatal("revoked scope committed") }
if got.ActiveCalls > 2 { t.Fatal("cluster model account limit exceeded") }
if unknown.ReservedTokens != before.ReservedTokens { t.Fatal("unknown usage refunded") }
if secondSettlement.Changed { t.Fatal("usage settled twice") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(AuthorizationCommit|ServiceRoleScope|BudgetReservation|RedisFailover)' -count=1 -v
```
预期：FAIL，白名单函数不存在、service角色可伪造scope，或旧进程内配额不能通过跨连接竞争；用屏障精确控制撤回/提交先后，不用sleep猜时序。
- [ ] **Step 3 — 最小实现：** 按公共协议2.1实现SECURITY DEFINER函数，专用无登录owner、限定search_path/表名、撤销PUBLIC EXECUTE，session_user绑定service。Session/Run/context与guard同事务；提交先取得SHARE guard锁，撤回用排他锁；准入一开始取预算guard UPDATE锁，排序resource行后登记预占，不锁升级。共享预算只有一账，未配置有限预算拒绝新执行；后台scope独立。Redis Lua以原attempt ID操作，MaxRetries=-1、等待最多2秒且不超剩余期限；不可用停止新外呼但允许原结果结算。占用按hold_until保守保留，未知不退款，重建以PG generation互斥。 CreateSession/RevokeSession由platformpg实现既有auth.Store签名，Session与guard在仓储一次事务提交；AuthService不在事务外单独更新guard。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：两种撤回竞争顺序、跨scope拒绝、多资源锁序、重复结算及Redis重建/丢许可均有真实PG/Redis证据；额度不随副本数增加。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A03 原子授权撤回、共享预算与Redis准入`）。

### A04：固定期限的Run接纳、入口Job认领与终态

**OpenSpec mapping:** 实施子项3.1.4；3.1、3.2；agent-execution/执行互斥与终态、全局期限；task-recovery/重启后判定恢复能力。
**Depends:** A01～A03。

**Files:**
- Create: backend/internal/repository/platformpg/runs.go
- Create: backend/internal/repository/platformpg/jobs.go
- Create: backend/internal/platformrun/service.go
- Create: backend/internal/platformrun/types.go
- Create: backend/tests/runtimeintegration/admission_job_test.go
- Modify: backend/internal/model/run.go

**Interfaces:**
- Consumes: A03 control客户端，A02 platform连接与legacy reader；仍由服务端model.Scope认证身份。
- Produces: platformrun.Service.Admit(ctx context.Context, scope model.Scope, in SubmitInput) (runtimev1.RunView,error)；platformpg.ClaimEntry(ctx,workerID string) (runtimev1.JobLease,bool,error)；RenewEntry(ctx context.Context,lease runtimev1.JobLease) (runtimev1.JobLease,error)；FinishRun(ctx context.Context,ref runtimev1.ExecRef,in FinishInput) error。SubmitInput为conversation_id/idempotency_key/content/request_hash；FinishInput为真实status/code/最终消息/partial refs。RunView/JobLease/状态事件DTO由A01 run.go定义，JobLease.Ref为ExecRef、checkpoint版本、固定deadline。

- [ ] **Step 1 — 先写有意义的失败测试：** TestAdmitIdempotencyAndConflict；TestDeadlineStartsAtAdmission；TestExpiredEpochCannotRenewOrCommit；TestTerminalMessageEventAtomicity。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if duplicate.ID != first.ID || messages != 1 { t.Fatal("duplicate admission") }
if deadline.Sub(admittedAt) != 300*time.Second { t.Fatal("deadline reset") }
if renewed.Lease.Epoch != claimed.Lease.Epoch { t.Fatal("renew changed epoch") }
if oldCommitErr == nil || terminalTransitions != 1 { t.Fatal("stale writer accepted") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(AdmitIdempotency|DeadlineStarts|ExpiredEpoch|TerminalMessage)' -count=1 -v
```
预期：FAIL，旧Run从执行开始计时、仅attempt围栏，或接管/终态消息不是当前Job授权下的原子提交。
- [ ] **Step 3 — 最小实现：** 接纳事务按control→用户额度→会话→Run/Job顺序，保存原Session、固定deadline、预算配置、用户消息及可靠事件。waiting仍占会话非终态互斥。用PG clock_timestamp在锁后复核期限；领取新epoch、续租不改epoch且不续回过期租约。完成/失败/超时与最终消息/seq同事务，拒绝旧version/owner/epoch；活动执行额度按原配置守持久上限，不以页面在线决定存活。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：同key/不同内容/不同活动请求边界清晰，排队计时、过期接管、双终态竞争和存储失败回滚通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A04 固定期限的Run接纳、入口Job认领与终态`）。

### A05：无计划共享查询、证据和唯一重试层

**OpenSpec mapping:** 实施子项3.1.5；3.1、3.3；travel-evidence全部；agent-execution/网络重试责任唯一；RAG任务5.3/5.4的平台侧。
**Depends:** A01、A03、A04。

**Files:**
- Create: backend/internal/toolquery/service.go
- Create: backend/internal/toolquery/types.go
- Create: backend/internal/toolquery/retry.go
- Create: backend/internal/repository/platformpg/tools.go
- Create: backend/internal/repository/travelpg/tools.go
- Create: backend/internal/client/mcp/http.go
- Create: backend/internal/client/mcp/evidence.go
- Create: backend/internal/handler/internal_evidence.go
- Create: backend/internal/toolquery/service_test.go
- Create: backend/tests/runtimeintegration/shared_query_test.go
- Modify: backend/internal/client/providerhttp/resilience.go
- Modify: backend/internal/client/providerhttp/resilience_test.go
- Modify: backend/internal/agenttools/web_search_tool.go
- Create: backend/internal/runtimeports/attempts.go
- Create: backend/internal/client/llm/metered.go
- Create: backend/internal/client/llm/metered_test.go
- Create: backend/internal/repository/platformpg/outbound.go
- Create: backend/internal/repository/travelpg/outbound.go
- Modify: backend/internal/client/llm/client.go
- Create: backend/internal/evidence/reader.go
- Create: backend/internal/repository/platformpg/evidence_imports.go
- Create: backend/internal/repository/travelpg/evidence_imports.go

**Interfaces:**
- Consumes: A03每次实际尝试登记/资源许可，A04有效ExecRef；既有train/hotel/weather/websearch客户端；RAG四工具v1/远端HTTP参数。
- Produces: toolquery.Service.Execute(ctx context.Context, ref runtimev1.ExecRef, in QueryInput) (runtimev1.SharedToolEvidenceRecord,error)；FindSaved(ctx context.Context,ref runtimev1.ExecRef,callKey string) (record,bool,error)。QueryInput{CallKey,Domain,Mode,Provider string;Arguments json.RawMessage;SourceRefs []runtimev1.SourceRef;ForceRefresh bool}；QueryStore由platformpg/travelpg各自实现。mcp.HTTPClient.CallEvidence(ctx context.Context,callKey string,query PolicyQuery) (EvidenceResponse,error)；QueryInput→供应方参数适配保持注册Schema，PolicyQuery携query/逻辑库/top_k/可选time_context。 mcp.EvidenceResponse{Evidence model.PolicyEvidence;Execution,Raw json.RawMessage}保存可选执行追踪与完整原响应，不能因legacy解码丢版本/降级信息。runtimeports.AttemptStore.Reserve(ctx context.Context,ref runtimev1.ExecRef,in control.AttemptInput) (control.Attempt,error)、Settle(ctx context.Context,ref runtimev1.ExecRef,in control.Settlement) error、FindModelOutput(ctx context.Context,ref runtimev1.ExecRef,callKey string) (json.RawMessage,bool,error)、SaveModelOutput(ctx context.Context,ref runtimev1.ExecRef,attemptID,callKey string,output json.RawMessage) error由各本地仓储实现，准入同时核验自己的Job/Task epoch。runtimeports.WithCall(ctx context.Context,ref runtimev1.ExecRef,logicalKey string,attemptNo int) context.Context只供程序注入。llm.NewMetered(base em.ToolCallingChatModel,store runtimeports.AttemptStore,permits permit.Service,resourceKeys []string) em.ToolCallingChatModel包装Generate/Stream/WithTools，完整模型输出先在所属messages中持久化，再返回上层；已存输出按原logical key和指纹复用。 PolicyQuery{Query,Collection,QueryType string;TopK int;TimeContext json.RawMessage}。evidence.Locator{OriginService,OriginID,ContentHash string;OriginRevision int64}与View{ReferenceID string;ResultRevision int64;Record runtimev1.SharedToolEvidenceRecord}在evidence/reader.go定义；Reader.Resolve(ctx context.Context,ref runtimev1.ExecRef,evidenceID string,revision int64) (View,error)，统一读本地事实或已验证imports，selection另校验candidate_id。

- [ ] **Step 1 — 先写有意义的失败测试：** TestDirectQueryHasNoPlan；TestRetryFourAttemptsAcrossRestart；TestSavedEvidenceNoRecompute；TestMCPHTTPResultLimitAndScope；TestMeteredModelPreservesWithToolsAndSavedOutput；TestCrossServiceEvidenceIdentityAndRevokedSource。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if plans != 0 || delegations != 0 { t.Fatal("direct query created plan") }
if actualAttempts != 4 || logicalCalls != 1 { t.Fatal("retry multiplied/reset") }
if savedEvidence != replayedEvidence || extraRequests != 0 { t.Fatal("recomputed saved result") }
if hotel.Price != nil || hotel.Booked { t.Fatal("invented price/booking") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/toolquery ./internal/client/providerhttp ./internal/client/mcp ./internal/client/llm -count=1
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go test ./tests/runtimeintegration -run 'Test(DirectQuery|RetryFour|SavedEvidence|MCPHTTP|CrossServiceEvidence)' -count=1 -v
```
预期：FAIL，旧查询依赖TripPlan、HTTP只有3次总尝试、未持久化attempt或只支持stdio；故意断在结果提交前/后区分可重算与应复用。
- [ ] **Step 3 — 最小实现：** 查询事实由实际所属schema持有，tool_call/evidence同事务成功；空成功也保存证据，权限/模式/输入/新鲜度核验后复用。所有网络在事务外；本地timer实现U(0,1/2/4秒)，持久化次数和next_retry_at，SDK隐藏重试关闭。providerhttp可供仅一次物理发送，由本服务拥有重试计数，禁止两层循环。网页继续零自动重试。HTTP MCP池按服务身份复用、禁独立SSE/默认重连，完整响应上限1,000,000字节包括SDK外壳，超限明确失败。GET /internal/v1/evidence/:id/versions/:rev验证服务凭据＋scope并返回不可变body/hash；调用方校验后持久传输快照，不改原事实。模型/辅助模型同样通过Metered包装核验并登记，每次实际调用受原Run预算/账户共享许可约束；WithTools返回仍被包装。每个阶段/轮次的稳定callKey由执行记录派生，不用随机transport ID；完整输出及调用关联先保存到本地messages再返回，崩溃后优先复用。新runtime模型客户端每次只发一次HTTP，由上层累计四次策略负责；不叠加旧NewTravel内部retry。流式已显示后失败只保存草稿/失败，不重开流，其Runner断言放A08。 远端引用为remote:<注册service slug>:<SHA256原id UTF-8>，slug≤32使引用≤160字符；表存完整原身份/原payload/hash，命名空间冲突拒绝，candidate_id不变。导入只存evidence_imports，正文权威在原服务；首次委派由可信caller/scope将原平台引用映射给专业上下文。已存快照仍检查当前授权/来源撤回/新鲜度，不伪造本地供应方调用。
模型预算预留必须使用已验证的模型输入硬上限与实际设置的输出max_tokens；20,000上下文估算不能充当费用硬上界。供应方用量未知时保留预留，不退款或重置尝试；辅助模型与WithTools派生实例同样计量。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：两入口和Executor都能复用接口，纯查询无计划；真实尝试由独立HTTP接收计数核对，已存结果不重算，权限/未知价格/响应大小边界通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A05 无计划共享查询、证据和唯一重试层`）。

### A06：正式计划、原子Patch及Task局部失效

**OpenSpec mapping:** 实施子项3.1.6；3.1、3.3；tripplan-lifecycle全部；travel-planning全部；travel-source-fidelity/新协议。
**Depends:** A01～A03、A05。

**Files:**
- Create: backend/internal/service/travelplan/service.go
- Create: backend/internal/service/travelplan/patch.go
- Create: backend/internal/service/travelplan/versions.go
- Create: backend/internal/repository/travelpg/plans.go
- Create: backend/internal/repository/travelpg/actions.go
- Create: backend/internal/service/travelplan/patch_test.go
- Create: backend/tests/runtimeintegration/plan_commit_test.go

**Interfaces:**
- Consumes: A01 PlanDefinition/PlanPatch/PlanSnapshot/CommitResult/EvidenceRef；A03授权；A05证据及受控跨服务来源。
- Produces: travelplan.Service.Create(ctx context.Context,ref runtimev1.ExecRef,operationID string,definition runtimev1.PlanDefinition) (runtimev1.CommitResult,error)；ApplyPatch(ctx context.Context,ref runtimev1.ExecRef,operationID string,patch runtimev1.PlanPatch) (runtimev1.CommitResult,error)；View(ctx context.Context,ref runtimev1.ExecRef,planID string) (runtimev1.PlanView,error)；AcceptQueryResult(ctx context.Context,ref runtimev1.ExecRef,request runtimev1.ExecutionRequest,view evidence.View) (runtimev1.ExecutionResult,error)。

- [ ] **Step 1 — 先写有意义的失败测试：** TestPatchAtomicGraphAndCAS；TestTaskDefinitionInvalidatesAllItsItems；TestSelectionAndRefreshVersions；TestUnknownPriceAndProvenance；TestCommittedActionReplay。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if changedTaskRevision != oldTaskRevision+1 || unchangedTaskQueriedAgain { t.Fatal("wrong invalidation") }
if selectionTaskRevision != oldTaskRevision || refreshedGeneration != oldGeneration+1 { t.Fatal("version conflation") }
if failedPatchChangedPlan || repeatedOperationAddedRevision { t.Fatal("non-atomic replay") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/service/travelplan -count=1
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go test ./tests/runtimeintegration -run 'Test(PatchAtomic|TaskDefinition|SelectionAndRefresh|UnknownPrice|CommittedAction)' -count=1 -v
```
预期：FAIL，新定义/Patch边界不存在，或旧hard/soft/整份计划升版规则不符合新Task/Item语义。
- [ ] **Step 3 — 最小实现：** 稳定Stop/Leg/Task/Item ID、金额整数/未知null、来源优先与结构合法性分离。首版完整定义、后续允许字段set/unset与稳定ID业务操作；私有副本顺序应用后整图校验，删除必须同Patch处理引用，拒绝任意JSON Pointer/数组下标。本地事务提交snapshot/current pointer/actions结果/选择失效/执行安排；同operation同hash返回原回执。有效Task定义含路线/要求，变化全Task重查，无关Task保留；selection不升查询版本，refresh只增generation，同值Patch不造修订。程序不做hard/soft候选资格过滤。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：原多城fixture、往返关联、最小单项安排、缺参最小计划及所有Patch冲突/回放/迟到证据测试通过；未知与部分结果不伪成功。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A06 正式计划、原子Patch及Task局部失效`）。

### A07：Travel持久Task受理与四工具HTTP MCP

**OpenSpec mapping:** 实施子项3.1.7；3.1、3.2；agent-delegation/独立MCP、最终failed；travel-execution/专业任务系统恢复。
**Depends:** A01～A03、A05、A06。

**Files:**
- Create: backend/internal/traveltask/service.go
- Create: backend/internal/traveltask/http.go
- Create: backend/internal/repository/travelpg/tasks.go
- Create: backend/internal/repository/travelpg/threads.go
- Create: backend/internal/traveltask/http_test.go
- Create: backend/tests/runtimeintegration/travel_task_test.go

**Interfaces:**
- Consumes: A01七个运行类型及严格codec；A03 scope/issuer/owner服务绑定；A02 Travel库；A06计划/线程归属。
- Produces: traveltask.Service.Submit(ctx context.Context,callerID string,meta runtimev1.TrustedTravelMeta,in runtimev1.TravelTaskSubmit) (runtimev1.TravelTaskReceipt,error)；Find(ctx context.Context,callerID string,meta runtimev1.TrustedTravelMeta,in runtimev1.TravelTaskFind) (runtimev1.TravelTaskReceipt,error)；Status(ctx context.Context,callerID string,meta runtimev1.TrustedTravelMeta,taskID string) (runtimev1.TravelTaskReceipt,error)；Result(ctx context.Context,callerID string,meta runtimev1.TrustedTravelMeta,taskID string) (runtimev1.TravelTaskResult,error)；Handler(cfg ServerConfig) http.Handler。ServerConfig只接注入的服务身份解析器/TLS边界/请求上限/服务。 TravelTaskLease沿用A01 runtimev1.TravelTaskLease，本服务不另外定义相同名字的执行权类型。

- [ ] **Step 1 — 先写有意义的失败测试：** TestTravelConcurrentSubmitAndInputConflict；TestTravelAcceptedResponseLost；TestTravelFinalFailureNeverRequeues；TestTravelMCPMetaAndCallerRotation。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if tasksCreated != 1 || first.TaskID != duplicate.TaskID { t.Fatal("duplicate task") }
if differentHashErr == nil || forgedMetaErr == nil { t.Fatal("invalid scope/input accepted") }
if failedRetry.Status != "failed" || failedRetry.TaskID != first.TaskID { t.Fatal("final failure restarted") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/traveltask -count=1
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go test ./tests/runtimeintegration -run 'TestTravel(Concurrent|Accepted|FinalFailure|MCPMeta)' -count=1 -v
```
预期：FAIL，当前没有独立持久Task/MCP服务器，或按RPC起goroutine会产生多个Task。
- [ ] **Step 3 — 最小实现：** 认证caller从Bearer映射稳定service ID，轮换key不变caller。先control授权，再Travel唯一约束INSERT ON CONFLICT DO NOTHING；后续语句读已提交原行比hash，不冲突UPDATE覆盖状态。受理事务持久queued/线程/一次上下文追加后才响应。create新线程，continue读正式计划/原线程并校验；并发同线程返回受控冲突。官方Go SDK远端Streamable HTTP，禁GET进度SSE，四工具参数仅适配层构造，_meta另传；最大1,000,000字节，未终态result_pending。无自动结果/幂等GC，过期请求不创建新Task。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实两个HTTP客户端与PG并发同key唯一、不同hash冲突、受理丢响应找回、最终failed不重置、caller/key轮换与元数据伪造拒绝均通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A07 Travel持久Task受理与四工具HTTP MCP`）。

### A08：Eino桥接、完整父断点与waiting联合提交

**OpenSpec mapping:** 实施子项3.1.8；2.2、3.1、3.2；task-recovery/内部异步等待与有效断点；agent-delegation上下文配对。
**Depends:** A01～A04、A07。

**Files:**
- Create: backend/internal/agent/agents/travelbridge/agent.go
- Create: backend/internal/agent/einoruntime/job_runner.go
- Create: backend/internal/agent/einoruntime/capture_store.go
- Create: backend/internal/repository/platformpg/delegations.go
- Create: backend/internal/agent/einoruntime/job_runner_test.go
- Create: backend/tests/runtimeintegration/waiting_checkpoint_test.go

**Interfaces:**
- Consumes: A01 wire/ExecRef；A04 JobLease；固定Eino StatefulInterrupt/AgentAsTool/ResumeWithParams；A07只是协议对端，首次挂起不调用它。
- Produces: travelbridge.New(cfg BridgeConfig) adk.ResumableAgent；BridgeConfig传可信ExecRef、固定原调用身份及结果读取接口，不含活连接SessionValues。einoruntime.RunJob(ctx context.Context,in JobInput) (JobOutcome,error)，JobInput{Lease runtimev1.JobLease;Agent adk.Agent;Messages []*schema.Message;ResumeTargets map[string]any}；JobOutcome含Complete/Waiting/Failed及完整checkpoint bytes/hash/interruptTargets/真实最终消息。platformpg.SuspendParent(ctx,ref runtimev1.ExecRef,in Suspension) error，Suspension绑定checkpoint版本/原tool_call_id/委派/唯一Outbox。

- [ ] **Step 1 — 先写有意义的失败测试：** TestParentSuspendAtomicAndNoPrematureSubmit；TestNestedResumeOriginalToolAndNamespacedState；TestWaitingHasNoLeaseOrSlot；TestCheckpointEncodingFailurePublishesNothing；TestPartialModelStreamNotSpliced。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if job.Status != "waiting" || job.Lease.WorkerID != "" || occupiedEntrySlots != 0 { t.Fatal("waiting retained execution") }
if failedCommit && (outboxRows != 0 || remoteSubmits != 0) { t.Fatal("work escaped failed suspend") }
if resumedToolCallID != originalToolCallID || replayedModelDecisions != 0 { t.Fatal("wrong resume") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/agent/einoruntime -count=1
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go test ./tests/runtimeintegration -run 'Test(ParentSuspend|NestedResumeOriginal|WaitingHas|CheckpointEncoding|PartialModelStream)' -count=1 -v
```
预期：FAIL，旧TurnLoop未请求暂停即跳过checkpoint、Run级Store单独提交；隔离探针绿不代表当前仓储的联合事务已存在。
- [ ] **Step 3 — 最小实现：** 桥Run只产生不可变委派并主动StatefulInterrupt；排空最外层事件并取得完整bytes，无编码/Set错误后再用一次PG事务保存checkpoint/调用映射/Delegation/Outbox/waiting、清租约。提交成功执行退出，续租协程停止，释放entry槽；失败无发布资格。Bridge Resume只读取已收集原结果并输出确定性JSON，未满足target继续中断。job/call/plan命名空间、兼容指纹和版本独立；多个并行中断聚合依赖，不隐式Resume全部，不复用旧RequestPause产品通道。 入口模型使用A05 Metered包装并注入稳定模型callKey；流式首个可见delta后断连，保存真实已有草稿/原因，不透明重试拼接另一输出，最终快照不得伪造完整成功。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实PG回滚、保存前后杀停、新Runner读取持久bytes、两次连续调用/错误target/多依赖隔离、等待期间无续租及释放槽全部证明。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A08 Eino桥接、完整父断点与waiting联合提交`）。

### A09：Outbox轮询、Kafka接手与持久延迟Dispatch

**OpenSpec mapping:** 实施子项3.1.9；3.1、3.2；agent-delegation/子Agent提交的持久延迟重试；task-recovery/可靠调度/Outbox多实例。
**Depends:** A03、A04、A07、A08；A00真实Kafka。

**Files:**
- Create: backend/internal/repository/platformpg/outbox.go
- Create: backend/internal/repository/platformpg/dispatch.go
- Create: backend/internal/dispatch/publisher.go
- Create: backend/internal/dispatch/consumer.go
- Create: backend/internal/dispatch/submitter.go
- Create: backend/internal/dispatch/retry.go
- Create: backend/tests/runtimeintegration/dispatch_test.go

**Interfaces:**
- Consumes: A08联合提交的委派与Outbox；A01 DispatchCommand/TravelTaskSubmit；A07四工具；A03 control_rpc授权。
- Produces: platformpg.ClaimOutbox(ctx,owner string,limit int) ([]OutboxLease,error)；MarkPublished(ctx context.Context,lease OutboxLease) error；AcceptDispatch(ctx context.Context,msg runtimev1.DispatchCommand,owner string) (DispatchLease,bool,error)；ScheduleSubmitRetry(ctx context.Context,lease DispatchLease,next time.Time) error。OutboxLease/DispatchLease各含rowID/owner/epoch/lease期限/seq及不可变原消息引用；dispatch.Publisher.Tick(ctx) error；Consumer.PollOnce(ctx) error；Submitter.Execute(ctx,lease DispatchLease) error。

- [ ] **Step 1 — 先写有意义的失败测试：** TestOutboxPublishMarkCrashAndStaleOwner；TestOffsetAckAfterDurableHandoff；TestDispatchAckThenCrashRecovers；TestNotBeforeAndFourSubmitAttempts；TestKafkaRebalanceCannotSkipUnacceptedOffset。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if ackedBeforePGCommit || ackedAcrossFailedGap { t.Fatal("unsafe offset") }
if now.Before(notBefore) && sends != 0 { t.Fatal("early retry") }
if submitAttempts != 4 || remoteTaskCount != 1 { t.Fatal("identity/retry reset") }
if oldPublisherMarkedNewLease { t.Fatal("old publisher overwrote lease") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(OutboxPublish|OffsetAck|DispatchAck|NotBefore|KafkaRebalance)' -count=1 -v
```
预期：FAIL，新Kafka/Outbox/Dispatch尚不存在；DB异常、broker确认后落库前退出及offset后退出分别注入，不能靠重复提交总是成功的假broker证明。
- [ ] **Step 3 — 最小实现：** Publisher只定时索引扫描，短事务SKIP LOCKED认领/增epoch，事务外Produce、broker ack后有界围栏标记；不发Redis主动唤醒。franz关闭auto commit、PollRecords有界＋BlockRebalanceOnPoll、PG持久接手后只提交连续安全offset并AllowRebalance，MCP不在poll循环。producer AllISRAcks/幂等并显式AllowIdempotentProduceCancellation、有限RecordDeliveryTimeout/重试，晚回调不得标记新owner；未完成物理发送仍占本地发送容量。Submitter按原key查/提交，RPC超时不当未执行；失败事务累计次数并写唯一not_before Outbox/新dispatch_seq，抖动只生成一次，不重置预算，不让巡检另建一套重试。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实broker重复/重分配、DB失败不ack、ack后PG恢复、未到期不发、旧seq跳过、4次耗尽及同key唯一Task有证据；无通知也能发现工作。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A09 Outbox轮询、Kafka接手与持久延迟Dispatch`）。

### A10：Travel专业Plan-Execute与持久轮次

**OpenSpec mapping:** 实施子项3.1.10；3.1、3.3；travel-execution全部；travel-source-fidelity全部。
**Depends:** A03、A05～A07；ContextReader由A10定义、A12实现，本任务用授权夹具实现端口，最终装配必须等A12。

**Files:**
- Create: backend/internal/agent/agents/travelplanner/agent.go
- Create: backend/internal/agent/agents/travelplanner/planner.go
- Create: backend/internal/agent/agents/travelplanner/executor.go
- Create: backend/internal/agent/agents/travelplanner/replanner.go
- Create: backend/internal/agent/agents/travelplanner/progress.go
- Create: backend/internal/agent/agents/travelplanner/prompts/planner.txt
- Create: backend/internal/agent/agents/travelplanner/prompts/replanner.txt
- Create: backend/internal/agent/agents/travelplanner/agent_test.go
- Create: backend/tests/runtimeintegration/professional_worker_test.go

**Interfaces:**
- Consumes: A06正式PlanView/提交回执，A05共享查询及A07专业Task；固定Eino prebuilt/planexecute.Config允许自定义Planner/Executor/Replanner，MaxIterations=8。
- Produces: travelplanner.New(ctx context.Context,cfg Config) (adk.ResumableAgent,error)；Config含Model、PlanService、QueryService、TaskStore、ContextReader及LimitSnapshot。ContextReader.Load(ctx,ref runtimev1.ExecRef,request runtimev1.TravelToolArgs) (runtimev1.PlannerInput,error)端口由本任务定义，A12实现。travelplanner.Worker.Execute(ctx,lease runtimev1.TravelTaskLease) error；TaskStore.Claim/Renew/SaveProgress/Finish按本地Task与epoch操作，TravelTaskLease由A01定义。 TaskStore.Claim(ctx context.Context,workerID string) (runtimev1.TravelTaskLease,bool,error)、Renew(ctx context.Context,lease runtimev1.TravelTaskLease) (runtimev1.TravelTaskLease,error)、SaveProgress(ctx context.Context,lease runtimev1.TravelTaskLease,progress PlanningProgress) error、Finish(ctx context.Context,lease runtimev1.TravelTaskLease,result runtimev1.TravelTaskResult) error；PlanningProgress由本任务定义phase/round_count/no_progress_count/fingerprint/checkpoint及已存模型输出引用，绑定task epoch。

- [ ] **Step 1 — 先写有意义的失败测试：** TestProfessionalPlanExecuteCommitsBeforeQuery；TestEightRoundsTwoNoProgressPersisted；TestTaskRecoveryReusesQueryAndAction；TestPolicyUnknownDoesNotInventConstraints；TestDeterministicExecutorHasNoModelSelection。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if queriedBeforeCommit || executorModelCalls != 0 { t.Fatal("executor bypassed plan") }
if maxRounds > 8 || unchangedRoundsAtFinish != 2 { t.Fatal("loop boundary") }
if resumedRounds < persistedRounds || duplicateActionRevision { t.Fatal("recovery reset") }
if unknownPriceBecameZero || claimedBooked { t.Fatal("invented fulfillment") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./internal/agent/agents/travelplanner -count=1
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go test ./tests/runtimeintegration -run 'Test(ProfessionalPlan|EightRounds|TaskRecoveryReuses|PolicyUnknown|DeterministicExecutor)' -count=1 -v
```
预期：FAIL，旧Main/Verifier/总结与固定默认值不能满足正式计划驱动；仅同进程框架成功而未提交业务也应失败。
- [ ] **Step 3 — 最小实现：** Planner只提完整定义并调用确定性提交Chain；Executor从已提交Task展开就绪Item，ToolsNode前后确定性适配，独立Item可并行、仅真实depends_on串行，逐项先存工具事实再接受执行项，缺参blocked不猜。Replanner仅受控Patch或finish，prior_replans从actions/revisions/执行报告投影。使用planexecute.New的自定义三个Agent，不用带LLM工具选择的默认Executor；持久阶段/原模型输出/轮次/实质指纹。初始Planner不计轮，连续2轮无实质变化或累计8轮结束；同值刷新/回执/措辞不算进展。轮次计数与对应报告/决定/回执一次提交；Task running独立续租，同步RAG保持专业槽，停止/无进展从正式事实投影部分结果。提示分层、schema和实际配置入指纹，不恢复不兼容旧断点。 Planner/Replanner/政策准备辅助模型均接A05 MeteredModel，通过当前task/阶段/轮次原身份登记与复用已存输出；不得直接绕行Config.Model产生未计账调用。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：脚本模型覆盖正常/局部修改/能力缺口/协议修正/8轮/2无进展/进程接管；真实调用账包含所有阶段，不沿用旧通常两次调用判据。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A10 Travel专业Plan-Execute与持久轮次`）。

### A11：结果收集、父ready与逐层恢复巡检

**OpenSpec mapping:** 实施子项3.1.11；3.1、3.2；task-recovery全部；agent-delegation/最终failed及迟到结果。
**Depends:** A04、A07～A10。

**Files:**
- Create: backend/internal/maintenance/results.go
- Create: backend/internal/maintenance/recovery.go
- Create: backend/internal/repository/platformpg/results.go
- Create: backend/internal/repository/travelpg/recovery.go
- Create: backend/tests/runtimeintegration/result_recovery_test.go

**Interfaces:**
- Consumes: A07状态/结果RPC；A08原依赖/interrupt映射；A09已接手Dispatch记录；A10正式专业终态。
- Produces: maintenance.Collector.Tick(ctx context.Context) error；platformpg.CollectResult(ctx context.Context,delegationID string,result runtimev1.TravelTaskResult) (madeReady bool,err error)；maintenance.Recovery.Tick(ctx) error；travelpg.RecoverExpired(ctx context.Context) error。Tick各循环独立配额/超时，结果保存＋依赖满足＋父ready同事务；重认领仍由A04。

- [ ] **Step 1 — 先写有意义的失败测试：** TestCollectResultAtomicAndOnce；TestFailedTaskPartialResultReturnsToOriginalParent；TestTerminalOrExpiredParentNeverRevives；TestWaitingNotTreatedAsDeadWorker；TestResumeGrantNotDuplicated。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if collectedRows != 1 || readyTransitions != 1 { t.Fatal("duplicate resume grant") }
if failedResult.TaskID != originalTaskID || createdReplacementTask { t.Fatal("failed task restarted") }
if parentTerminal && madeReady { t.Fatal("late result revived parent") }
if waitingWasLeaseReclaimed { t.Fatal("waiting classified as worker death") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(CollectResult|FailedTaskPartial|TerminalOrExpired|WaitingNot|ResumeGrant)' -count=1 -v
```
预期：FAIL，旧恢复等待用户继续、无远端原结果收集事务，或简单扫描无租约把waiting误接管。
- [ ] **Step 3 — 最小实现：** 分别扫描全局过期、失权running、Outbox发布租约、未知Dispatch提交、已受理专业Task及waiting依赖；先复核资格，再查原Task/原结果。父终态/失权/期限到仅审计，不ready。最终failed保留真实部分结果交原调用，不自动换key或新Task；多依赖只满足对应target，合适时定向恢复。全局超时一次终结并关闭Run guard；Travel只由自己接管租约，平台状态查询不替其续租。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：结果获取前后/保存前后退出、重复/迟到/失败结果、无租约waiting、旧代次/失权及多依赖恢复全部有事务证据。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A11 结果收集、父ready与逐层恢复巡检`）。

### A12：线程上下文、用户记忆与后台十轮维护

**OpenSpec mapping:** 实施子项3.1.12；3.1、3.3；agent-delegation上下文/目录/短期记忆；travel-memory全部；context-isolation全部。
**Depends:** A03～A07、A10端口。

**Files:**
- Create: backend/internal/contextv1/builder.go
- Create: backend/internal/contextv1/planner_input.go
- Create: backend/internal/memoryv1/service.go
- Create: backend/internal/memoryv1/maintenance.go
- Create: backend/internal/repository/platformpg/memory.go
- Create: backend/internal/repository/platformpg/context.go
- Create: backend/internal/repository/travelpg/context.go
- Create: backend/internal/handler/internal_context.go
- Create: backend/tests/runtimeintegration/context_memory_test.go

**Interfaces:**
- Consumes: A01 ContextSummary/PlannerInput/UserMemorySnapshot/Memory操作DTO，A03独立维护scope及来源guard；A06正式计划目录，A07 TravelThread/上下文增量。
- Produces: contextv1.Builder.BuildEntry(ctx context.Context,ref runtimev1.ExecRef,currentUserText string) ([]model.Message,error)；contextv1.PlannerReader.Load实现A10 ContextReader；memoryv1.Service.Write(ctx context.Context,ref runtimev1.ExecRef,operationID string,args runtimev1.MemoryWriteArgs) (runtimev1.MemoryWriteResult,error)；memoryv1.Maintenance.Tick(ctx) error。内部GET /internal/v1/context/entry-delta、/memory/related以scope+目标线程/固定上界返回受控投影，不转发Cookie。

- [ ] **Step 1 — 先写有意义的失败测试：** TestContextBudgetAndCompletePairing；TestTravelThreadIncrementOnceAndSourceRevoked；TestMemoryTenRoundsReviewedVersusSummarized；TestEmptyMemoryAndExplicitRetractionCAS；TestBackgroundMemoryDoesNotBlockAnswer。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if contextTokens > 20000 || currentRunWasSummarized || duplicateCoveredMessages { t.Fatal("context boundary") }
if newValidRounds == 9 && reviewCalls != 0 { t.Fatal("early memory review") }
if noSummaryNeeded && summarizedCursor != beforeSummarized { t.Fatal("review claimed summary") }
if emptyUpdates && memoryRevision != beforeRevision { t.Fatal("empty update created revision") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(ContextBudget|TravelThreadIncrement|MemoryTen|EmptyMemory|BackgroundMemory)' -count=1 -v
```
预期：FAIL，当前5轮关键词分析、旧来源混合摘要及Run级上下文不能满足10轮/双游标/独立线程。
- [ ] **Step 3 — 最小实现：** 入口当前用户原文只注入一次；20,000估算token/80%检查/每次最多3个旧完整轮次，系统/工具Schema计入预算，不拆当前Run/工具配对；摘要与覆盖CAS一致。首次必要快照，继续只传相关主线程增量并本地加载原Travel历史；上下文追加/上界幂等。目录只取授权相关计划，模型判断新建/继续，程序校验对象/版本，不固定追问。长期记忆用户统一、领域筛选、最新整份快照再按条目来源/撤回过滤；explicit工具即时确定性提交，inferred不覆盖explicit。每主Thread累计10个成功有效用户轮次，Maintenance独立scope/预算/租约先必要性检查，再按需MemoryAgent，空更新成功推进summarized但不加修订，无需总结只推进reviewed，失败不伪造覆盖。明确重分析后台化，clear/retract/来源排除不经子线程复活。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：10轮边界、异步无阻塞、两个入口独立计数、CAS/清除竞争、撤回及旧来源隔离、摘要失败/覆盖不重复、子线程一次增量和超预算真实错误通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A12 线程上下文、用户记忆与后台十轮维护`）。

### A13：独立进程装配与双入口真实委派

**OpenSpec mapping:** 实施子项3.1.13；3.1、3.3；agent-delegation双入口/只读边界；identity-and-conversations全部；streaming-and-delivery真实交付。
**Depends:** A00～A12。

**Files:**
- Create: backend/Dockerfile
- Create: frontend/Dockerfile
- Create: .dockerignore
- Create: deploy/local/compose.application.yaml
- Create: backend/cmd/api/main.go
- Create: backend/cmd/worker/main.go
- Create: backend/cmd/travel/main.go
- Create: backend/cmd/maintenance/main.go
- Create: backend/internal/bootstrap/runtime.go
- Create: backend/internal/bootstrap/travel.go
- Create: backend/internal/handler/runtime.go
- Create: backend/internal/agent/agents/travelentry/agent.go
- Create: scripts/runtime-up.ps1
- Create: scripts/runtime-down.ps1
- Create: backend/tests/runtimeintegration/process_assembly_test.go
- Modify: backend/cmd/server/main.go
- Modify: backend/internal/router/router.go
- Modify: backend/internal/application/chat/service.go
- Modify: backend/internal/agent/agents/general/agent.go
- Modify: backend/internal/service/conversation/service.go
- Create: backend/internal/repository/platformpg/identity.go
- Create: backend/internal/repository/platformpg/conversations.go
- Create: backend/internal/repository/platformpg/settings.go

**Interfaces:**
- Consumes: A04接纳/Job；A05共享工具；A08桥；A09分发；A10专业worker；A11恢复与A12上下文/后台记忆。
- Produces: bootstrap.NewAPI(ctx,cfg config.RuntimeConfig) (*App,error)；NewWorker/NewTravel/NewMaintenance同签名，App包含Handler（仅HTTP角色）/Run(ctx) error/Close()。application/chat.RuntimeService以platformrun及受控目录/查询端口协调，API不执行Agent。runtime-up.ps1 -Profile <隔离配置> -Topology dual；runtime-down只终止自己PID/启动标识对应进程。 新platformpg实现当前auth/user/conversation/settings各service消费的既有Store签名，使用platform限定SQL及原ID；禁止调用旧public仓储写入。

- [ ] **Step 1 — 先写有意义的失败测试：** TestAPINeverStartsAgent；TestBothEntrypointsShareQueryAndDelegateOnce；TestSameSessionAcrossAPIsAndRevocation；TestMissingCapabilityIsHonest；TestRuntimeProcessesRequireMigrations。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if apiModelCalls != 0 || apiToolCalls != 0 { t.Fatal("API executed agent") }
if entry == "travel" && generalAgentCalls != 0 { t.Fatal("travel routed via General") }
if userRuns != 1 || professionalTasks != 1 { t.Fatal("duplicate outer run") }
if unmigratedStarted || runtimeRoleExecutedDDL { t.Fatal("startup migration bypass") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(APINever|BothEntrypoints|SameSessionAcross|MissingCapability|RuntimeProcesses)' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go build ./cmd/...
```
预期：FAIL，旧bootstrap同进程执行、独占锁/自动DDL、General不委派且RAG为stdio；只删锁会在DB权限/任务持久测试失败。
- [ ] **Step 3 — 最小实现：** 按用户Docker选择制作应用镜像与Compose，API/entry与Dispatch Worker/Travel/Maintenance分别装配，应用使用容器内服务地址，迁移仍为独立一次性入口；API仅HTTP鉴权/接纳/查询/SSE，平台Worker entry与Dispatch逻辑池各2槽独立限流，Travel每实例1槽，自主租约/恢复；Maintenance各循环独立，不被后台模型阻塞。每Go进程PG池≤4，连接按角色创建，禁止单凭据写两个业务schema。General/差旅entry共享查询/设置/记忆端口，规划走同桥，不额外General跳转；正式计划只读经MCP/受控投影，修改/选择必须专业提交。复用Cookie/PG Session/CSRF与同源校验，页面page_lease只接入。显式新二进制不执行DDL；旧cmd/server迁移为API兼容入口，旧执行器/恢复循环不能同时启动。启动脚本只用隔离profile、隐藏后台进程窗口、跟踪自身PID，不加载/覆盖.env或杀其他程序。 装配全部模型/辅助模型为A05 MeteredModel，QueryService/Model AttemptStore各用所属schema连接；平台Conversation删除/清除必须与context_guard同事务。 配置每个Role仅加载所需连接，禁止为使用control而给Travel运行角色platform业务DML。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：2 API/2 Worker/2 Travel/1 Maintenance实进程可分摊，跨API登录/撤销与两入口流程通过；缺Travel/RAG时明确不可用，其他能力仍可用。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A13 独立进程装配与双入口真实委派`）。

### A14：跨API SSE、草稿快照与简化交互

**OpenSpec mapping:** 实施子项3.1.14；3.1、3.3；streaming-and-delivery全部；context-isolation/迟到事件；agent-execution/逐轮问答。
**Depends:** A04、A11、A13。

**Files:**
- Create: backend/internal/repository/platformpg/events.go
- Create: backend/internal/runstream/service.go
- Create: backend/tests/runtimeintegration/stream_reconnect_test.go
- Modify: backend/internal/handler/sse.go
- Modify: frontend/src/api/client.ts
- Modify: frontend/src/api/events.ts
- Modify: frontend/src/hooks/useRunEvents.ts
- Modify: frontend/src/hooks/usePageLease.ts
- Modify: frontend/src/components/RunControls.tsx
- Modify: frontend/src/App.tsx
- Modify: frontend/src/App.test.tsx
- Create: frontend/src/hooks/useRunEvents.test.tsx
- Create: deploy/local/nginx.runtime.conf

**Interfaces:**
- Consumes: A04可靠Run seq和最终消息；A03鉴权；Redis提示但非事件权威；A13进程与API。
- Produces: runstream.Service.Subscribe(ctx,scope model.Scope,runID string,after int64) (Subscription,error)；Subscription.Close()；前端PlatformAPI.subscribe(runID: string,onEvent: (event: RunEvent) => void,signal: AbortSignal,after?: number): Promise<void>，readEvents保留seq/stream_id/lease_epoch/delta_seq/output_version/covered_delta_seq；GET /runs/:id/events支持Last-Event-ID/after和cursor_ahead。

- [ ] **Step 1 — 先写有意义的失败测试：** TestSSEAcrossAPIWithLostNotification；TestStreamEpochSnapshotAndCursorAhead；TestSlowSubscriberAndSessionExpiry；前端reconnect_cursor_and_snapshot、old_delta_ignored、no_pause_resume_cancel_controls。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if replayedSeqSkipped || disconnectedRunStopped { t.Fatal("connection controls execution") }
if oldEpochDeltaVisible || finalAnswerDuplicated { t.Fatal("stream corrupted") }
if slowClientBlockedWorker || expiredSessionStillSubscribed { t.Fatal("subscription boundary") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test ./tests/runtimeintegration -run 'Test(SSEAcross|StreamEpoch|SlowSubscriber)' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
npm --prefix ../frontend run test -- --run
```
预期：FAIL，前端未带after重连、旧RunControls仍存在，进程内通知不能跨API；用断流＋丢Redis通知验证PG补读。
- [ ] **Step 3 — 最小实现：** 持久事件seq与状态/结果同事务，通知只提示回读，保持2秒鉴权/补读和5秒写deadline。delta不推进SSE ID，100ms聚合、普通快照最多每1秒一次，最终/错误及时落库，单帧≤256KiB，超大用授权output_ref。前端快照覆盖covered delta、切stream丢旧epoch、终态停重连；游标超前重查权威快照。关闭页面只断订阅。移除暂停/继续/整轮取消按钮、路由和旧冲突文案，保留内部超时/停止及历史原因。Nginx关闭SSE buffering/cache/gzip，15秒注释心跳/60秒读超时；慢队列64关闭单连接。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：前后端测试与浏览器双API断流/重开人工检查通过；不发新Run、不回放模型；旧输出不能污染当前，用户看不到内部Planner日志。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A14 跨API SSE、草稿快照与简化交互`）。

### A15：全拓扑故障矩阵与真实RAG同步接缝

**OpenSpec mapping:** 实施子项3.1.15；3.2、3.3；原local-validation-scenario/交付故障矩阵；RAG 5.2～5.5联合验收。
**Depends:** A00真实隔离环境、A01～A14；RAG计划R07（包含R00～R06）。

**Files:**
- Create: backend/tests/runtimee2e/process_fixture_test.go
- Create: backend/tests/runtimee2e/fault_matrix_test.go
- Create: backend/tests/runtimee2e/rag_sync_test.go
- Create: scripts/runtime-fault-matrix.ps1
- Create: deploy/local/fault-cases.json
- Create: docs/reports/runtime-v1/fault-matrix.json

**Interfaces:**
- Consumes: 两API/Worker/Travel＋Maintenance；真实PG/Kafka/Redis；R07真实HTTP RAG（Milvus/模型单独标明）；脚本模型/供应方自身允许>平台额度并独立计数。
- Produces: runtime-fault-matrix.ps1 -Profile <隔离配置> -Case All -RequireServices；报告schema为case_id/input_hash/instance_id/injection/call_counts/db_assertions/actual_dependencies/status/error/artifact_refs，不记录密钥。跨仓联调只执行一次，此报告供R10引用，不复制实现。

- [ ] **Step 1 — 先写有意义的失败测试：** TestRuntimeFaultMatrix；TestRAGSyncHoldsTravelSlotAndLease；TestRAGEvidenceCommitCrashAndRetryBudget；TestRedisLossAndSchemaPermissions。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if parentWaitingSlots != 0 || travelDuringRAGSlots != 1 { t.Fatal("wrong sync/async ownership") }
if ragJobs != 0 || ragTopics != 0 { t.Fatal("RAG became agent/job") }
if clusterActualModelConcurrency > 2 || ragConcurrentAdmitted > 1 { t.Fatal("shared cap exceeded") }
if successWithoutRequiredDependency || skippedRequiredCase { t.Fatal("false release pass") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
pwsh -File ../scripts/runtime-fault-matrix.ps1 -Profile $runtimeProfile -Case All -RequireServices
```
预期：FAIL，新矩阵首先在注入预期故障时显示对应断言失败，例如刻意去掉提交后恢复扫描应丢工作；实现恢复后重跑同case转绿。环境缺失必须失败，不能以未启动服务的连接错误代替TDD红灯。
- [ ] **Step 3 — 最小实现：** 真实进程杀停：父checkpoint事务前后、broker ack后PG标记前、offset接手前后、MCP受理响应丢失、Travel租约过期/旧epoch恢复、结果收集前后、not_before前后、最终failed部分结果、Run300秒到期/Session撤回、多资源争用、Redis single及Sentinel专项、schema越权和SSE缺口。RAG用真实HTTP/Milvus/已验证模型跑完整证据，再断连/重启/平台证据提交前后对比调用账；本地取消不伪称原生计算立即停止。检查scope/原调用/所有计数不重置。QPS扫描选做，不拿同机容器说跨主机高可用。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：矩阵必做项无SKIP、逐项有真实状态与独立调用计数；失败原始输出保留，模拟供应方与真实依赖范围清晰；完成原3.2/3.3及RAG联合接入证据。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A15 全拓扑故障矩阵与真实RAG同步接缝`）。

### A16：新库部署演练、全量审核、规格核对与归档收尾

**OpenSpec mapping:** 实施子项3.1.16；3.4、3.5；全部原规格及两计划完成条件。
**Depends:** A00～A15、RAG R09/R10；用户选择的执行方式持续有效。

**Files:**
- Create: backend/tests/runtimeintegration/cutover_test.go
- Create: docs/reports/runtime-v1/verification.json
- Create: docs/runtime-v1-runbook.md
- Modify: scripts/verify.ps1
- Modify: .github/workflows/ci.yml
- Modify: README.md
- Modify: openspec/changes/refactor-agent-kernel-plan-execute/tasks.md

**Interfaces:**
- Consumes: 所有任务已验证的代码/测试/记录；A02全新数据库初始化、A15联合报告、R10 RAG最终报告；活跃搜索变更保留未完成状态。
- Produces: verify-runtime.ps1 -Case All -RequireCases全量报告；runbook精确启动/迁移/回退/故障复现。OpenSpec任务证据逐项映射，verify工作流存在时运行，否则人工矩阵；archive后本分支包含同步规格与归档文件。

- [ ] **Step 1 — 先写有意义的失败测试：** TestFreshDeploymentHasNoLegacyTasks；TestFreshDatabaseReplay；TestRollbackPreservesNewRuntimeFacts。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```go
if importedLegacyTasks != 0 { t.Fatal("unexpected legacy task import") }
if newFactsDeletedForRollback { t.Fatal("destructive rollback") }
if unverifiedTaskCheckedDone || failedRequiredTestArchived { t.Fatal("false completion") }
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
go test -race ./... -count=1 -timeout 180s
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
go build ./cmd/...
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
npm --prefix ../frontend run test -- --run
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
npm --prefix ../frontend run build
```
预期：FAIL，先写切换/回退行为回归，空库装配、版本检查或新事实安全回退尚未满足；全量审核阶段不人为制造全量失败来冒充TDD。
- [ ] **Step 3 — 最小实现：** 隔离库演练全新部署、重复初始化、独立迁移与新事实安全回退；停止旧执行者，清理旧启动迁移/页面控制路径，不导入旧库。按选择的单代理自审或多代理规格＋质量复审完成代码审查；Linux Go1.26.x执行race，Windows未提供race环境时不假称通过。真实模型/供应方冒烟使用显式live profile及有限预算，未执行如实列出，不能让脚本模型冒充质量证据。读取OpenSpec verify/归档指导并核对CLI能力，逐条需求/场景/任务/测试证据，重要偏差修复重验。只在两change完成各自必要条件后分别archive，尊重RAG→Agent引用依赖，不自动归档未完成搜索变更。最后使用finishing-a-development-branch给出合并/PR/保留选择，按用户选择操作。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：任务完成证据、相关全量测试/构建、代码审查与规格一致性全部满足；未完成项不归档。两仓库各自分支/提交和归档位置可追溯，用户决定最终集成方式。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(runtime): A16 新库部署演练、全量审核、规格核对与归档收尾`）。

## 规格覆盖与计划自审

下表逐项对应原change的有效Requirement；具体场景的红灯测试、边界和完成条件见对应任务。它是执行索引，不替代原specs。

| 原能力与Requirement | 实施及验证任务 |
| --- | --- |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 独立MCP子Agent接入 | A01、A07、A08、A09、A11 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 子Agent提交的持久延迟重试 | A09 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 双入口共享查询与按需规划 | A05、A13 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 正式计划的只读与修改边界 | A06、A13 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 全新数据库部署与新请求切换 | A02、A16 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 会话信息与业务状态延续 | A12 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 差旅子线程上下文延续 | A07、A12 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 计划与差旅子线程对应 | A07、A12 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 已有计划修改时加载正式状态 | A06、A10 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 当前入口 Agent 按需定位计划 | A12、A13 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 模型判断新建或继续 | A12、A13 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 专业结果统一表达 | A11、A13 |
| [agent-delegation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-delegation/spec.md) / 会话短期记忆与压缩 | A12 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 执行互斥与终态 | A04 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 共享额度和累计调用预算 | A03、A05 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 网络重试责任唯一 | A05、A09 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 全局期限与多阶段有效性校验 | A04、A09、A11 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 逐轮问答与简化交互 | A13、A14 |
| [agent-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/agent-execution/spec.md) / 运行限制与系统收尾 | A10、A11 |
| [context-isolation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/context-isolation/spec.md) / 历史保留与上下文排除 | A02、A12、A16 |
| [context-isolation](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/context-isolation/spec.md) / 迟到事件不能污染新任务 | A11、A14 |
| [identity-and-conversations](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/identity-and-conversations/spec.md) / 用户数据隔离 | A03、A13 |
| [identity-and-conversations](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/identity-and-conversations/spec.md) / 会话历史与管理 | A02、A13 |
| [identity-and-conversations](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/identity-and-conversations/spec.md) / 多实例登录与有效执行授权 | A03、A13 |
| [streaming-and-delivery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/streaming-and-delivery/spec.md) / 流式订阅与权威查询 | A14 |
| [streaming-and-delivery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/streaming-and-delivery/spec.md) / 可复现交付与真实验证 | A00、A15、A16 |
| [streaming-and-delivery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/streaming-and-delivery/spec.md) / 跨实例事件补读与输出快照 | A14 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / 重启后判定恢复能力 | A08、A10、A11 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / 断点不保证外部副作用回滚 | A05、A11 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / 可靠调度与逐层恢复 | A09、A10、A11、A15 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / 问答执行与页面连接分离 | A13、A14 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / 内部异步等待与有效断点 | A08、A11 |
| [task-recovery](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/task-recovery/spec.md) / Outbox多实例认领与巡检兜底 | A09、A11 |
| [travel-evidence](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-evidence/spec.md) / 共享查询与真实能力边界 | A05、A06 |
| [travel-evidence](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-evidence/spec.md) / 共享证据与有界复用 | A05、A06 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 统一执行范围内的差旅委派 | A07、A13 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 计划驱动的有限执行 | A10 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 计划动作预检与可恢复推进 | A06、A10 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 计划执行的无进展保护 | A10 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 重新规划的授权范围与来源优先 | A06、A10、A12 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 失败分类与有界重试 | A05、A10 |
| [travel-execution](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-execution/spec.md) / 专业任务的内部等待与系统恢复 | A07、A10、A11 |
| [travel-memory](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-memory/spec.md) / Maintenance 判断长期记忆维护必要性 | A12 |
| [travel-memory](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-memory/spec.md) / 用户统一记忆与领域按需读取 | A12 |
| [travel-memory](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-memory/spec.md) / 相关长期记忆由程序装配 | A12 |
| [travel-memory](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-memory/spec.md) / 对话归纳的覆盖范围与一致提交 | A12 |
| [travel-memory](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-memory/spec.md) / 偏好和摘要遵循任务隔离 | A12 |
| [travel-planning](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-planning/spec.md) / 面向请求范围的结构化计划 | A06、A10 |
| [travel-planning](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-planning/spec.md) / 计划变更的约束校验与依赖失效 | A06、A10 |
| [travel-source-fidelity](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-source-fidelity/spec.md) / 历史基线追溯与新协议边界 | A02、A16 |
| [travel-source-fidelity](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-source-fidelity/spec.md) / 新计划的完整需求与来源审查 | A06、A10 |
| [travel-source-fidelity](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-source-fidelity/spec.md) / 计划修正的有限性与真实结果 | A06、A10 |
| [travel-source-fidelity](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-source-fidelity/spec.md) / 新决策角色的运行时职责一致 | A10、A12 |
| [travel-source-fidelity](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/travel-source-fidelity/spec.md) / 新角色重试与恢复的可核验性 | A05、A10、A11 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 多城市行程关联 | A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 按请求范围建立统一计划 | A05、A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 完整创建与增量修改 | A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 要求来源与模型排序依据 | A06、A10 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / Task 定义与 Item 查询动作 | A06、A10 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 查询标记与 Task 版本 | A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 执行依据与事实分离 | A05、A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 版本化结果复用 | A05、A06 |
| [tripplan-lifecycle](../../../openspec/changes/refactor-agent-kernel-plan-execute/specs/tripplan-lifecycle/spec.md) / 可追溯修订与原子提交 | A06 |

自审结果：63项有效Requirement均有映射；25项REMOVED要求不重新实施，旧数据库保留已由用户取消；A02/A16负责新库初始化及新版本回归保护。

- 已核对所有任务都有文件、消费/产出接口、命名测试及关键断言、正确红灯原因、最小实现、验证命令和交付门槛；计划初次交付时实施checkbox均未开始；当前状态见各步骤及OpenSpec tasks。
- 已核对跨任务类型归属与方法名；A10先定义ContextReader、A12实现，A05统一模型/工具调用计量及证据导入，避免向后依赖造成接口空洞。
- 依赖图无环：R07→A15→R10→A16；每个Review Focus都落到对应任务测试，服务缺失与SKIP不能算完成。
- 本节为实施前的计划自审记录；用户已选择单代理。当前代码/测试/环境进度以OpenSpec tasks和docs/reports/runtime-v1为准，不能将计划材料验证当成新链路验收。

计划材料验证（2026-11-06）：两change的`openspec validate --strict --no-interactive`均通过；原契约校验43份正例/47项反例通过；28个任务共140个未开始步骤、84项Requirement映射、链接/文件归属/先创建后修改及Markdown结构检查通过。这些结果不代表产品实现、数据库迁移或真实服务验收通过。

