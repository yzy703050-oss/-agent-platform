# Go Agent Platform MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for user-selected single-agent execution, or superpowers:subagent-driven-development only after explicit multi-agent selection. Steps use checkbox syntax for tracking.

**Goal:** 实现可运行的普通聊天平台，具有用户隔离、真实 Eino 工具执行、页面关闭暂停、持久化断点和手动恢复；差旅仅预留入口。

**Architecture:** Service 与 Agent 并列，Application 协调聊天用例。GeneralAgent 使用 Eino ChatModelAgent + TurnLoop，Store 经 Repository 持久化；平台管理 Run，内核管理执行与恢复。

**Tech Stack:** Go + Gin + pgx + PostgreSQL；Eino v0.9.0-beta.1；React + Vite + TypeScript；Go testing、httptest、前端组件测试和真实 PostgreSQL 集成测试。

**Spec:** [proposal](../../../openspec/changes/build-agent-platform-mvp/proposal.md)、[specs](../../../openspec/changes/build-agent-platform-mvp/specs/)、[design](../../../openspec/changes/build-agent-platform-mvp/design.md)、[tasks](../../../openspec/changes/build-agent-platform-mvp/tasks.md)。本计划只展开执行步骤；行为以 specs 为准，技术决定以 design 为准。

## Global Constraints

- 单实例 Go + PostgreSQL；正式服务不静默回退内存数据库。
- Eino v0.9.0-beta.1；标准 schema.Message；复用执行和恢复机制，首版不开放 shell/file/RAG/MCP/审批。
- 页面心跳每 5 秒，租约 20 秒，安全停止等待上限 15 秒；默认活动执行 120 秒、8 次模型迭代、工具 10 秒。
- Service 与 Agent 并列，受保护业务写入经 Service，Run/消息/事件/Checkpoint 经 Store→Repository。
- paused 仅表示执行已停止且本次有效断点已持久化；没有有效断点必须明确 interrupted。
- 旧任务被新问题替代后历史仍可查看，context_eligible=false，旧断点失效，后续上下文不再使用它。
- 只在用户继续时启动恢复；页面内切换与 SSE 取消不暂停；关闭发起页触发暂停。
- 平台所有个人数据绑定认证身份，模型与客户端参数不能覆盖 user_id。
- 单代理由当前代理连续实施并整体自审；多代理需用户明确选择。当前还未选择，不能擅自分派。
- 尚无 Git 仓库；不伪造提交记录。实施获准后先初始化本地 Git 并提交已审阅文档；不推送远程。

## Review Focus

1. 暂停写断点失败或强制停止后没有有效断点：不能显示可恢复；任务 1、5 必测。
2. 旧 checkpoint/旧回调在新 attempt 或新任务后到达：不能覆盖状态或污染上下文；任务 2、4、5 必测。
3. 两次提交/恢复同时到达或服务重启：不能双执行；任务 2、4、5 必测。
4. 浏览器换订阅、后台页、不同标签页或登出：不能把会话订阅当页面租约；任务 5、6、7 必测。
5. 模型返回错误、凭据缺失、工具参数伪造或用户越权：必须拒绝且不泄密；任务 3、4、6 必测。

## Shared Interfaces

以下是模块衔接约束，任务 1 定义契约类型；业务内部小接口由消费者定义，不扩大为万能接口。

- model.Scope{UserID, SessionID, PageID string}；身份来源为服务端，不从消息正文反序列化。
- model.Run{ID, ConversationID, UserID, PageID, Status string; Attempt, Version int64; ContextEligible bool}。
- contracts.RunInput{Scope model.Scope; Run model.Run; Messages []model.Message; Resume bool; CheckpointID string; Limits contracts.Limits}。
- contracts.Event{RunID, Type string; Attempt int64; Seq int64; Payload json.RawMessage}；Payload 不得装入凭据。
- contracts.ExecutionResult{FinalText string; ErrorCode string}；contracts.PauseResult{Stopped, CheckpointSaved bool; ErrorCode string}。
- contracts.Execution.Start(context.Context, func(contracts.Event) error) (contracts.ExecutionResult, error)、Pause(context.Context) (contracts.PauseResult, error)、Cancel(context.Context) error。
- contracts.Agent.NewExecution(context.Context, contracts.RunInput) (contracts.Execution, error)；registry 按入口存 Agent，不保存共享可变用户状态。
- application/chat.SubmitInput{ConversationID, Content, IdempotencyKey, PageID string}。
- application/chat.Application.Submit(context.Context, model.Scope, SubmitInput) (model.Run, error)。
- manager.Manager.Resume(context.Context, model.Scope, runID string) (model.Run, error)；Pause 与 Cancel 使用同样的 scope/runID 参数返回 model.Run。
- agenttools.SettingsReader.Get(context.Context, model.Scope) (model.UserSettings, error)；SettingsService 实现。
- ports.RunStore 接收 model.AdmitRunInput、model.FinishRunInput、model.FinalizePauseInput，返回 model.Run；由任务 2 定义这些存储输入字段及事务前置状态。
- ports.MessageStore.ListContext(context.Context, model.Scope, conversationID, runID string) ([]model.Message, error)。
- ports.CheckpointStore.Get(context.Context, model.Scope, runID string) (model.Checkpoint, bool, error)、Save(context.Context, model.CheckpointWrite) error、Invalidate(context.Context, model.Scope, runID string, attempt int64) error。
- Eino CheckPointStore 的具体 Get/Set/Delete 由 general/checkpoint_store.go 桥接，不能把 Eino 类型引入 model、Service 或 Handler。

---

## Task 1: 固定依赖与 Eino 恢复探针

**OpenSpec mapping:** 1.1；agent-execution/普通聊天与授权工具；task-recovery/安全暂停与有效断点；design D2、D6。
**Files:** 创建 backend/go.mod、go.sum、internal/model/{run,message,scope,checkpoint}.go、agent/contracts/{agent,events}.go、agent/agents/general/{runner,checkpoint_store}.go，以及该目录下 eino_checkpoint_test.go、testmodel_test.go。
**Interfaces:** 产出 Shared Interfaces 中的执行契约；general.NewExecution 封装真实 Eino TurnLoop；测试 Store 不进入产品启动路径。

- [ ] 先写 TestCheckpointResumeAcrossExecutionInstances：可控模型请求两个只读工具，在第一个工具完成的框架安全边界请求停止；确认保存，再销毁执行对象、创建新对象并继续，断言已完成工具不重复且最终输出完成。
- [ ] 写 TestCheckpointSaveFailureNotResumable、TestResumeWithoutApprovalPayload、TestCheckpointConfigurationMismatch；失败断言为不可恢复、无需伪造 ApprovalResult、版本不兼容不启动。
- [ ] 运行 `go test ./internal/agent/agents/general -run 'TestCheckpoint|TestResume' -v`，记录预期编译失败/未实现或断言失败的具体原因，不能以网络失败代替红灯。
- [ ] 最小实现真实 Eino Agent/TurnLoop、可控测试模型与工具、Store 适配和 continue 回调；不自建 ReAct 循环、不靠 fake Execution 证明 Eino 可恢复。
- [ ] 重跑同一命令通过，记录使用版本和实际 API。若探针失败，先修订 design 与后续计划再继续。
- [ ] 更新实施记录与 OpenSpec 1.1；在已初始化 Git 中提交该交付。

## Task 2: PostgreSQL 模型、围栏与语义事务

**OpenSpec mapping:** 2.1；identity-and-conversations/用户数据隔离；agent-execution/请求幂等、执行互斥与终态；context-isolation/迟到事件；design D3、D4。
**Files:** migrations/001_platform.up.sql、001_platform.down.sql；config/config.go；repository/postgres/{db,migrate,auth,conversation,run,message,event,checkpoint}.go；agent/ports 下 Store 接口；tests/integration/repository_test.go。
**Interfaces:** 实现 ports.RunStore.AcceptRun、CompleteRun、SupersedePausedRun、FinalizePause；CheckpointWrite 必须包含 Scope、RunID、Attempt、版本元数据和 payload；所有操作校验 owner/attempt/status。

- [ ] 先写 TestConcurrentIdempotentAdmission、TestSameKeyDifferentBody、TestSameKeyDifferentPageReturnsOriginalOwner、TestSingleNonterminalRun、TestStaleAttemptRejected、TestCompleteTransactionRollback、TestSupersedeAtomicity、TestCrossUserCheckpointDenied。
- [ ] 在真实测试库运行 `go test ./tests/integration -run 'Test.*(Admission|Body|Run|Attempt|Transaction|Supersede|Checkpoint)' -v`，预期缺表/未实现失败；缺 TEST_DATABASE_URL 时记录未运行，不勾完成。
- [ ] 最小实现 pgxpool、迁移（包含 page_leases）、唯一索引、原子准入/完成/替代/暂停事务和 CAS；Schema 与 Eino payload 分开存储。数据库失败不能回退内存。
- [ ] 相同命令通过，并验证并发重复只产生一条消息、旧断点不能在失效后恢复；凭据不写日志。
- [ ] 更新任务 2.1 和执行记录，提交；数据库尚未可用时此任务保持未完成。

## Task 3: 身份、设置、会话与 HTTP 边界

**OpenSpec mapping:** 3.1；identity-and-conversations 全部；streaming-and-delivery/诊断与敏感信息保护；design D3、D9。
**Files:** service/{auth,user,settings,conversation}/service.go 及测试；middleware/{auth,csrf,logger}.go；handler/{auth,user,settings,conversation,agent}_handler.go；router/router.go；bootstrap/bootstrap.go；cmd/server/main.go。
**Interfaces:** AuthService.Register/Login/Logout、ConversationService.RequireAccess/List/Create/Rename/Delete、SettingsService.Get/Update；身份只由认证中间件构建 model.Scope，SettingsService 满足 SettingsReader。

- [ ] 先写 TestLoginLogoutRevokesSession、TestDuplicateAccount、TestCSRFMissing、TestCrossUserConversationAccess、TestStableHistoryCursor、TestDeleteActiveConversationConflict、TestTravelUnavailable。
- [ ] 运行 `go test ./internal/service/... ./internal/middleware/... ./internal/handler/... -v`，确认失败来自缺失行为。
- [ ] 最小实现 Cookie Session、CSRF、密码摘要、权限与设置规则、Gin API、固定入口会话和差旅不可用响应。
- [ ] 重跑通过，再运行真实数据库的 auth/conversation 集成测试；无数据库只报告单元结果。
- [ ] 检查错误响应不包含秘密，更新 3.1、记录并提交。

## Task 4: 普通聊天、工具、幂等与上下文

**OpenSpec mapping:** 4.1；agent-execution 全部；context-isolation 全部；design D5、D8、D10。
**Files:** application/chat/service.go；agent/manager/{manager,registry,run_manager}.go；agents/general/{agent,adapter,prompt}.go；agent/tools/{registry,adapter}.go；agent/memory/context.go；agenttools/settings_tool.go；client/llm/client.go；相关 _test.go。
**Interfaces:** 实现 Application.Submit、Agent.NewExecution 与 Manager 入口；ContextBuilder.Build(context.Context, model.Scope, conversationID, runID string) ([]model.Message, error)；模型 Client 根据服务端配置创建 Eino 模型。

- [ ] 先写 TestSubmitIdempotentStartsOnce、TestConcurrentConversationConflict、TestUserRunQuota、TestGetMySettingsIgnoresForgedUserID、TestUnknownToolDenied、TestIterationLimit、TestFinalPersistFailureNotSuccess。
- [ ] 写 TestSupersededRunVisibleButExcluded：历史含标记 old-task-secret，ContextBuilder 与后续模型输入均无该标记；正常更早任务仍存在；TestLateOldRunCannotPolluteNew。
- [ ] 运行 `go test ./internal/application/chat ./internal/agent/... ./internal/agenttools/... -v` 得到有意义失败。
- [ ] 最小实现准入、真实 Eino 模型/工具路径、工具授权、预算、上下文筛选及完成落库；GenInput 不重复保存用户消息。paused+新问题原子替代旧 Run，checkpoint 失效后再启动新执行。
- [ ] 重跑通过；配置真实模型时额外做实际工具调用冒烟，无 Key 就明确未验证，不能展示模拟回答充当真实运行。
- [ ] 更新 4.1、记录并提交。

## Task 5: 页面归属、暂停恢复与重启对账

**OpenSpec mapping:** 5.1；task-recovery 全部；context-isolation/明确区分继续与重新开始；design D6、D7。
**Files:** manager/lease_manager.go；application/chat/lifecycle.go；handler/{page,run}_handler.go；repository/postgres/page.go；agents/general/checkpoint_store.go；tests/integration/recovery_test.go；上述模块测试。
**Interfaces:** LeaseManager.Register/Heartbeat/Leave 必须绑定登录 Session 和 page_token；Manager.Pause/Resume/Cancel；启动 ReconcileInterrupted(context.Context) error 在获得单实例数据库锁后执行。

- [ ] 先写 TestPageSwitchKeepsRun、TestSSEDisconnectDoesNotPause、TestOwnerPageExpiryPauses、TestOtherPageCannotHeartbeatOwner、TestNewDocumentGetsDistinctPageIdentity、TestResumeTransfersPageOwner、TestLogoutPausesOwnedTasks；注入 clock，不依赖真实等待。
- [ ] 写 TestPauseCheckpointFailureInterrupted、TestResumeCASOnlyStartsOnce、TestRestartWithCheckpointWaitsForUser、TestRestartWithoutCheckpointInterrupted、TestIncompatibleCheckpointDoesNotExecute、TestOldAttemptCheckpointWriteRejected。
- [ ] 用 `go test ./internal/agent/manager ./internal/application/chat ./internal/handler -v` 确认红灯；数据库场景另跑 `go test ./tests/integration -run 'Test.*(Recovery|Restart|Resume|Pause)' -v`。
- [ ] 最小实现 5s/20s 租约、15s 安全停止预算、Eino Store 桥接、pause finalize、CAS 恢复和启动扫描。重开 GET 不调用内核；仅 resume 启动。检查实际 loop 退出与断点结果，不能仅因 stop 返回就标 paused。
- [ ] 重跑并验证真实 Eino + PostgreSQL 组合的销毁实例后恢复；未知外部副作用不自动重放。
- [ ] 更新 5.1、记录并提交。

## Task 6: SSE、部分输出与可观测性

**OpenSpec mapping:** 6.1；streaming-and-delivery 前三项；design D9。
**Files:** agent/manager/events.go；handler/sse.go；repository/postgres/event.go；middleware/logger.go；相关测试；基础 metrics 与 health handler。
**Interfaces:** EventHub.Subscribe(scope,runID) 提供有界订阅与取消；持久化事件 seq 不与临时 delta 混用；状态 GET 返回已有输出、错误和可恢复性。

- [ ] 先写 TestSubscribePersistRaceNoDuplicate、TestSlowSubscriberBounded、TestCompletionPublishedAfterCommit、TestPartialOutputSavedOnPause、TestModelErrorRedacted、TestUnauthorizedSSEDenied。
- [ ] 运行 `go test ./internal/agent/manager ./internal/handler ./internal/middleware -v` 确认预期失败。
- [ ] 最小实现先订阅后读持久事件并按 seq 去重、受限缓冲、临时文本增量、聚合输出存储、Request/Run/Attempt 日志及基础计数/耗时指标。
- [ ] 同命令通过，检查断开 SSE 不暂停任务；GET 状态可取到已提交终态和结果。
- [ ] 更新 6.1、记录并提交。

## Task 7: React 界面与真实 API 衔接

**OpenSpec mapping:** 7.1；所有规格的用户交互场景；design D7、D9。
**Files:** frontend/package.json、package-lock.json、index.html、vite.config.ts、tsconfig.json；src/{main,App}.tsx；src/api/{client,events}.ts；src/components/{AuthForm,ConversationSidebar,ChatWorkspace,RunControls,AgentPicker}.tsx；src/hooks/{usePageLease,useRunEvents}.ts；相关测试和 styles.css。
**Interfaces:** 严格使用 design D9 API；页面租约独立于当前 conversation/run 订阅；CSRF 来自登录结果，不将模型 Key 放入前端。
 
- [ ] 先写组件测试：testSwitchEntryCreatesConversation、testHistoryOpensOriginalAgent、testPausedOpenDoesNotResume、testResumeRequiresClick、testNewQuestionSupersedesPausedRun、testPageSwitchKeepsHeartbeat、testTravelHasNoFakePlan。
- [ ] 在 frontend 执行 `npm run test -- --run` 确认缺失交互红灯。
- [ ] 最小实现左侧历史和右侧入口/工作区、登录与设置、流式文字及工具状态、暂停/继续/结束按钮和页面级租约；切换 SSE 订阅不撤销页面归属。
- [ ] 执行 `npm run test -- --run` 与 `npm run build` 通过；在真实后端验证登录→聊天→切换→关闭→重开→继续→新问替代的完整操作。
- [ ] 更新 7.1、记录并提交。

## Task 8: 运行交付、最终验证与归档

**OpenSpec mapping:** 8.1；streaming-and-delivery/可复现交付与真实验证；design D11。
**Files:** .env.example、backend/Dockerfile、frontend/Dockerfile、compose.yaml、README.md、docs/runbook.md、docs/verification.md；新增 tests/integration/release_test.go 和发布验收脚本；必要时 CI 配置。
**Interfaces:** DB_DSN 运行配置与 TEST_DATABASE_URL 测试配置分开，不依赖开发机隐式数据库。

- [ ] 先写发布验收检查：缺数据库配置明确失败、迁移后 health ready、平台不静默用内存；未配置模型时 model_unavailable。
- [ ] 实现启动配置与脚本，执行 backend 的 `go test ./...`、`go vet ./...`，frontend 的 `npm ci`、`npm run test -- --run`、`npm run build`。
- [ ] 提供真实 PostgreSQL 后运行 `go test ./tests/integration -v -count=1` 和要求数据库必备的发布验收；分项保存输出。Windows race 工具链不可用时在 Linux CI 验证，不能称本机 race 已通过。
- [ ] 配置真实模型后记录真实聊天/工具/暂停恢复冒烟；无 Key 时保持此检查未完成，不要求用户将 Key 发到聊天。
- [ ] 按所选单/多代理方式做代码审核；对 specs 每个 Scenario 建立测试或人工证据映射，核对 tasks 不是只看勾选框。
- [ ] 运行 `openspec validate build-agent-platform-mvp --strict`；检查是否提供 verify 工作流。当前 CLI 无 verify 子命令：若未启用额外 verify 技能，使用人工规格核对并明确标注，不把 validate 当实现 verify。
- [ ] 仅在全部交付和真实验证满足完成条件后执行 `openspec archive build-agent-platform-mvp`，确认归档与主规格同步；之后进入分支收尾，按用户选择保留分支、合并或创建 PR。
- [ ] 更新 8.1、验证记录和交付状态。任何跳过项、环境阻碍或重要缺陷未解决时不得宣称 MVP 全部完成。

## Execution Record

2026-10-23：用户选择单代理执行；已建立 feat/agent-platform-mvp 本地分支并提交文档基线。任务 1 完成，证据见 docs/verification.md 与 docs/execution-ledger.md；任务 2–8 未完成。环境缺 PostgreSQL/Docker，真实数据库方案待确认；无真实模型凭据验证。

开始实施前记录用户审阅结论和执行方式，读取 OpenSpec apply 指导，再按任务实施。每项记录红灯命令/原因、绿灯命令/输出、受影响任务、提交及遗留限制；完成证据写入 docs/verification.md，并同步 OpenSpec tasks。

