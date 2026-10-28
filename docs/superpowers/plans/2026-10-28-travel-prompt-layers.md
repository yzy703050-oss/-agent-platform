# 差旅提示词分层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. 执行方式已选单代理；单代理由当前代理实施并整体自审，不自动派独立评审子代理。

**Goal:** 整理Go差旅实际提示输入，去除角色职责混杂和补丁互相覆盖，同时保留当前业务行为与Python迁移证据。

**Architecture:** 冻结原文保持；独立运行资源按职责组合。所有模型入口共用前缀组装和替换，工具参数帮助由工具层提供，资源及组装版本进入恢复指纹。

**Tech Stack:** Go 1.26、Eino v0.9.0-beta.1、go:embed、现有PostgreSQL及真实模型验收工具；不新增依赖。

**Spec:** `openspec/changes/refactor-travel-prompt-layers/specs/travel-source-fidelity/spec.md`、同change的`design.md`、`tasks.md`；沿用主规格`travel-planning`和`travel-execution`。本计划只展开上述规格和设计。

## Global Constraints

- 冻结Python提示、工具schema、SHA和旧报告逐字保留；不修改Python项目。
- 保持当前默认值优先级与proposal/soft、政策前置、Main一次及政策例外、总结当前计划范围、硬条件、事务、预算和事实绑定。
- 工具描述整理不能改变名称、参数schema、权限筛选及校验；不引入前端、依赖或数据库迁移。
- 新指纹使旧不兼容断点按既有规则拒绝；不自动部署或重启服务。
- 不以脚本模型通过代替真实模型验收，不把数据库/模型测试Skip记为通过。

## Review Focus

- 重试从Main进入总结时，旧Main规则被完整移除，原请求、错误及工具配对保留；任务2覆盖。
- 停止阶段不重新开放工具、默认规划或重排；任务1/2覆盖。
- 独立查询即使收到planning_defaults数据也不接收创建行程的指令；任务1/2/3覆盖。
- 硬一晚＋固定返程＋允许中转时，保留目的地task_id、硬活动及候选归属；任务3覆盖。
- 无flow入口及二次组装不产生重复公共层，工具运行副本不污染冻结profile；任务1/2覆盖。

## Task 1: 整理运行层、工具帮助和统一前缀

**Mapping:** OpenSpec 1.1；保真要求、职责一致全部场景及恢复幂等；design D1–D4。

**Files:**
- 新建：`backend/internal/agent/agents/travel/prompts/runtime/{main,planning_summary,verifier,planning,review,stopped}.txt`。
- 修改：`backend/internal/agent/agents/travel/prompt.go`；新建`prompt_layers_test.go`。
- 新建：`backend/internal/agenttools/travel/runtime_instructions.go`、`runtime_instructions_test.go`、`schemas/runtime_write_trip_help.txt`、`schemas/runtime_query_policy_help.txt`；修改`registry.go`的描述整理接入。
- 新建：`docs/travel-prompt-layers.md`，逐段记录旧规则→新维护层及保留/合并/迁移/已授权替代，不修改原始资源或schema。

**Interfaces:**
- `promptState{Role string; Stopped, ProtocolRecovery, InteractionRecovery bool}`。
- `composePromptPrefix(state promptState) []*schema.Message`：只返回按design D3固定顺序的受管规则消息。
- `replacePromptPrefix(messages []*schema.Message, state promptState) []*schema.Message`：替换受管前缀，保留非受管消息顺序及内容；重复调用结果相同。
- 工具包 `withRuntimeToolInstructions(profile string, tools []*schema.ToolInfo) []*schema.ToolInfo`：基于实际可见工具返回描述整理副本；名称、参数不变，不增减工具。

- [x] **Step 1: 先写失败测试。** 新建`TestTravelPromptLayers`，表驱动覆盖main/planning_summary/verifier/summary/stopped：恰有一个角色、一次公共回复，summary与stopped无规划动作层；main/总结保留现有软默认，规划总结没有“请求Main重规划”等旧肯定指令。新建`TestTravelPromptReplacementPreservesData`，输入含上下文system、摘要system、用户、assistant tool_calls、tool结果，两次替换后均逐字保留，旧Main规则不残留。工具包新建`TestRuntimeToolInstructions`，覆盖main/pre_policy/review/review_retry/summary/planning_summary/planning_recovery，核对名称/参数相同、原参数帮助一次、指定字段规则仅在对应工具、调用后SourceProfile冻结输出不变。
- [x] **Step 2: 确认RED。** 在`backend/`运行`go test ./internal/agent/agents/travel ./internal/agenttools/travel -run 'TestTravelPromptLayers|TestTravelPromptReplacementPreservesData|TestRuntimeToolInstructions' -count=1`；预期新接口尚不存在，或旧运行描述缺帮助而失败，保存实际原因。
- [x] **Step 3: 最小实现。** 按design D2逐段分配当前有效规则，写清楚默认值来源和角色职责；复用原reply/summary，旧Verifier与规划总结分开；新增层标记及D3组装/替换。工具描述只在Registry.Profile已有筛选后整理。停止层仅用现有结果作答；恢复提示不得再复制规划规则。完成规则映射，核对所有旧补充段落有归属和现有授权来源。
- [x] **Step 4: GREEN及保真。** 重跑RED命令，再运行`go test ./internal/agent/agents/travel ./internal/agenttools/travel -run 'TestTravelSourcePrompts|TestTravelSourceContracts|TestTravelPrompt|TestRuntimeTool' -count=1`；均PASS，原SHA不变。审查资源没有参数语法与业务规则大段重复，不按压缩字符数判完成。
- [x] **Step 5: 记录交付。** 在`docs/travel-prompt-layers-execution.md`记录RED/GREEN与规则映射，核对产物后勾选OpenSpec 1.1；保存可评审改动，按当前分支权限提交`refactor: separate travel runtime prompt responsibilities`。

## Task 2: 接入正常、恢复和重试的真实模型输入及指纹

**Mapping:** OpenSpec 2.1；提示在重试与恢复中可核验全部场景；design D3、D5。

**Files:**
- 修改：`backend/internal/agent/agents/travel/{flow,protocol,interaction_protocol,batch_gate,agent,prompt}.go`。
- 修改身份断言但保留业务断言：`source_flow_test.go`、`source_scenarios_test.go`、`interaction_test.go`、`summary_recovery_test.go`、`protocol_test.go`。
- 新建：`backend/internal/agent/agents/travel/prompt_layers_integration_test.go`、`prompt_fingerprint_test.go`。
- 修改：工具包`runtime_instructions.go`的版本材料接口；维护规则说明和执行记录。

**Interfaces:**
- 使用任务1的`composePromptPrefix`/`replacePromptPrefix`，从FlowState现有字段推导promptState；不新增产品状态机。
- `runtimePromptMaterial() string`及工具包`RuntimeToolInstructionMaterial() string`：按固定顺序返回有效文本和`prompt-layers-v1`版本，供现有Agent指纹使用；辅助提示及其他指纹因子保留。

- [x] **Step 1: 先写失败测试。** 用现有捕获真实Eino输入的模型扩展`TestMainRecoveryHandsToSummary`和`TestTravelInteractionInstructions`：普通首次、正常总结、参数拒绝、追问、无效协议、停止、无flow入口均只有适用层，实际工具与既有profile同名同参数，journal hash匹配真正请求。新建`TestTravelPromptFingerprint`验证全部运行资源/恢复文本和工具帮助均在材料中，版本或任一材料变化使兼容指纹变化。保留Main实际调用次数和summary范围断言。
- [x] **Step 2: 确认RED。** `go test ./internal/agent/agents/travel -run 'TestTravelPromptLayersIntegration|TestTravelPromptFingerprint|TestMainRecoveryHandsToSummary|TestTravelInteractionInstructions' -count=1`；预期flow仍使用旧补充、重试遗留Main层或无flow入口不一致，保存实际失败。
- [x] **Step 3: 最小接入。** flow构造规则前缀后照旧注入上下文与review；protocol及交互重新生成使用整体替换，保留未接受响应原因，停止不带动作层；batch_gate默认前缀同入口。指纹纳入运行层、工具说明和组装版本，旧冻结文本不再担当实际角色提示。只调整依赖旧提示身份的测试断言，冻结测试仍绑定原变量及SHA。
- [x] **Step 4: GREEN及回归。** 重跑RED；运行`go test ./internal/agent/agents/travel ./internal/agenttools/travel ./internal/service/trip -count=1 -timeout 180s`，正常两次、政策例外、恢复/重放、硬条件、候选引用、局部修改、缺口收尾保持。检查原始工具参数、profile过滤和预算代码没有随本次改动变化。
- [x] **Step 5: 记录交付。** 补充真实输入前缀、工具hash及指纹测试证据，勾选OpenSpec 2.1；按权限提交`refactor: unify travel prompt assembly and recovery`。

## Task 3: 完整验收、审查、规格核对与归档

**Mapping:** OpenSpec 3.1–3.3；本change全部场景，以及主规格默认条件/两次路径/局部修改/硬要求与事实绑定。

**Files:** 新建`docs/travel-prompt-layers-verification.md`、`docs/reports/travel-prompt-layers-interaction-2026-10-28.json`、`docs/reports/travel-prompt-layers-source-2026-10-28.json`；更新规则映射、执行记录及本change tasks；需要时仅扩展已有真实模型验收的输入审计断言，不放宽现有判据。

**Interfaces:** 使用既有`TestTravelInteractionModel`和`TestTravelSourceModel/modify_activity`，沿用现有凭据加载方式；只报告配置是否存在，禁止输出密钥/连接串。

- [ ] **Step 1: 整体静态及数据库验证。** 在backend运行`go test ./... -count=1 -timeout 180s`、`go vet ./...`、`go build -o ../.tmp/travel-prompt-layers-server.exe ./cmd/server`；启用既有`TEST_DATABASE_URL`和`REQUIRE_DATABASE_TESTS=1`后核对数据库项确已执行。若依赖缺失不可勾选通过；Windows race不支持时如实记录，适用Linux CI再验证，不能冒用旧CI成功。
- [ ] **Step 2: 真实模型交互。** 配置`REQUIRE_TRAVEL_INTERACTION_MODEL_TEST=1`、`TRAVEL_INTERACTION_REPEATS=1`、`TRAVEL_INTERACTION_OUTPUT`为上述交互JSON绝对路径；`TRAVEL_INTERACTION_CASES`置空执行九类场景。运行`go test ./tests/integration -run '^TestTravelInteractionModel$' -count=1 -timeout 20m`。检查缺省、显式覆盖、记忆、正常往返、多城、独立查询、政策、中转、酒店缺价；保存每次输入、实际角色、工具、Usage、拒绝和重复，失败不删样本、不调预算掩盖。
- [ ] **Step 3: 真实模型局部修改。** 配置`REQUIRE_TRAVEL_SOURCE_MODEL_TEST=1`、`TRAVEL_SOURCE_REPEATS=1`、`TRAVEL_SOURCE_OUTPUT`为上述来源JSON绝对路径，运行`go test ./tests/integration -run '^TestTravelSourceModel$/^modify_activity$' -count=1 -timeout 180s`。实际14:00→16:00、新修订、原车次引用保留、零供应方重查均通过。恢复测试开关，报告固定工具夹具边界，不把结果称作在线供应方验收。
- [ ] **Step 4: 审查与任务审核。** 按用户选择单代理整体自审或多子代理任务评审及最终评审，逐项检查spec场景→代码→测试→证据；任务和计划勾选分别核对，重要问题先修复并复验。对照git diff确认Python、冻结schema/提示、权限/状态机和其他活动change没有被改写；证据不足保留未完成状态。
- [ ] **Step 5: OpenSpec核对。** `openspec validate refactor-travel-prompt-layers --strict`。CLI没有verify命令，记录人工逐项核对需求、场景、任务及测试证据，不声称运行verify。勾选3.1/3.2必须有实际通过记录。
- [ ] **Step 6: 归档与分支收尾。** 前述通过后运行`openspec archive refactor-travel-prompt-layers --yes`，检查主规格仅同步本次delta并运行`openspec validate --all --strict`；补记3.3归档证据，确认归档包含于本次分支。使用finishing-a-development-branch流程呈现合并、PR或保留分支等选项，执行用户选择；不自动部署。

## Completion Conditions

有效运行提示无已识别相反规则、角色隔离和全入口一致；冻结证据不变；全部指定测试及真实模型验收通过，审查及人工规格核对无未解决重要问题；任务有证据后再归档。当前文件仅为待审阅计划，不表示上述步骤已经执行。

## Execution Choice

推荐单代理：两个实现任务共享资源、组装和指纹，连续实施上下文成本低，完成后整体自审。多子代理按任务交接最小上下文并安排独立规格/质量评审，评审更独立，但上下文与往返成本更高。由用户选择后启动apply；不默认派子代理。
