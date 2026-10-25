# 差旅无追问与常规执行流程 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for 用户选择的单代理执行，或在用户明确选择多子代理后使用 superpowers:subagent-driven-development。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按 Python 原设计使用注入记忆，使普通规划通常以 Main、Verify 两次完成，消除主动问卷与无进展额外调用。

**Architecture:** 保留 Go 原生 Eino 执行、自动查询作业、journal 和业务事务。独立补充规则明确角色职责与无追问交互；复用已有查询缓存和有界重排，成功工具回执直接呈现最终回复。政策前置和原版低频偏好归纳单列计量，不设置两次硬上限。

**Tech Stack:** Go、Eino v0.9.0-beta.1、PostgreSQL、PowerShell；不新增依赖。

**Spec:** `openspec/changes/prevent-travel-followup-questions/specs/`、`design.md`、`tasks.md`；技术依据见同目录 `technical-review.md`。

## Global Constraints

- 本计划只展开上述 OpenSpec 文档；用户要求覆盖旧“必要追问”授权，未知起点仍不虚构。
- 冻结的五份原版提示及工具 schema 不改，定向补充独立计入 fingerprint。
- 正常两次是效率目标；政策先于依赖它的 tripplan，局部修复及 request_replan 继续有界执行。
- 有效记忆直接注入；保留撤回、来源隔离、显式更新和每三条新增有效确认行程的原版低频归纳。
- 记录所有实际模型请求，包括政策、维护、压缩及重试；不得用两条阶段日志冒充总次数。
- 实施前从最新 main（复核时为 2a4b3c6）创建独立 codex/ 分支，不混入其他任务改动。不因选择单代理而自动派评审子代理。

## Review Focus

- 两次模型调用但只读历史和追问：必须判业务失败，Task 1/3 覆盖。
- 空记忆、撤回或被替代来源：不补读历史、不泄露，Task 1 覆盖。
- 未达阈值却重复分析，或同次偏好更新后 Verify 仍看旧值：Task 1 覆盖。
- 成功选择没带 reply 导致第三次模型，与有真实硬冲突不可提前收尾：Task 2 覆盖。
- 真正的政策前置、重排或证据刷新被误判成重复，或重复动作靠解释换词掩盖：Task 2/3 覆盖。

## Task 1: 记忆直注和角色行为约束

对应 OpenSpec tasks 2.1、2.2；travel-planning、travel-memory、travel-source-fidelity。

**Files:** 创建 `backend/internal/agent/agents/travel/prompts/no_followup.txt`、`interaction_test.go`；修改同目录 `prompt.go`、`agent.go`、`flow.go`；扩展 `auxiliary_test.go` 和 `backend/tests/integration/travel_memory_test.go`。仅确有必要时调整 `preference_agent.go` 的阈值/覆盖缺陷，不删除原归纳能力。

**Interfaces:** 沿用 `ReadPreferences func(context.Context) ([]pm.TravelPreference, error)` 和 `PreferenceAgent.Maintain(context.Context, pm.TravelExecution) error`；新增嵌入的 `interactionInstruction string`，所有实际角色输入包含一次。保持 `beforeModel` 的现有签名。

- [ ] **Step 1: 先写失败测试。** `TestTravelInteractionInstructions` 用真实 Eino 内核捕获 Main、Verifier、Summary 输入，断言都包含 interactionInstruction；新规划规则明确记忆已注入、为空不查历史、缺省直接建议、Verify 同次提交选择及回复。`TestTravelInjectedMemoryAcrossRoles` 断言有效值直接出现，两阶段无 read 工具，无偏好分析调用；显式本轮偏好更新后第二阶段看到新值；撤回/排除值不可出现。
- [ ] **Step 2: 确认 RED。** 在 backend 执行 `go test ./internal/agent/agents/travel -run 'TestTravelInteractionInstructions|TestTravelInjectedMemoryAcrossRoles' -count=1`。首次因缺 interactionInstruction 编译失败；加入空嵌入声明后再次确认因实际输入未含规则而断言失败。保留输出，不能把命名错误当行为 RED。
- [ ] **Step 3: 最小实现。** 独立补充规则，复用现有 `legacy_preferences/memories/preference_summary` 装配；各角色统一注入并计入 fingerprint。不要添加记忆读工具、额外分析模型或新的意图分类模型。现有 Maintain 在不够三条新增样本时必须零模型请求；已有覆盖样本不能再次触发归纳。
- [ ] **Step 4: 验证。** 运行上述测试及 `go test ./internal/agent/agents/travel ./internal/service/travelpreference ./internal/agent/memory -count=1`。扩展实际 PostgreSQL 隔离回归，覆盖空记忆、撤回、旧任务排除与显式更新；原版 `TestTravelSourcePrompts` 必须继续 PASS。低频维护测试预期：不足三条零调用，三条有效新增一次，再次检查仍一次。
- [ ] **Step 5: 自审并记录。** 更新任务证据及 OpenSpec tasks；在隔离分支仅提交本任务文件，建议提交信息 `fix: keep travel planning on injected memory and source defaults`。

## Task 2: 常规两次收尾，保留真实修复

对应 OpenSpec tasks 2.3；travel-execution。

**Files:** 创建 `backend/internal/agent/agents/travel/ready_reply_test.go`、`backend/internal/agenttools/travel/completion_reply.go` 及对应测试；修改 `agent.go` 的指纹和 `agenttools/travel/native_tools.go`；扩展 `backend/tests/integration/travel_workflow_test.go`。现有 source_scenarios_test.go 保留真实修复保护；查询缓存回归通过则不改 service。

**Interfaces:** 新增工具层纯判断 `completedStop(call PreparedCall, saved model.TripResult, operationID string) bool`；只有明确 allow_repair=false、完整匹配回执及全部校验通过时才在 Execute 中 BindReply，持久化 rendered_answer。`ReadyReply`、`request_replan` 和查询作业接口保持不变。技术复核修正：缺 reply 可能是还需语义修复，不能仅凭字段 complete 自动结束。

- [ ] **Step 1: 写边界 RED。** `TestTravelCompletedSelectionWithoutReplyFinishes`：Main propose、程序 query、Verify select 成功且明确 allow_repair=false、但省略 reply，预期恰好两次 Generate、零第三次总结、最终含真实保存的候选；现有路径应因尝试第三次模型而失败。配套 `TestCompletedExplicitStopReply` 覆盖缺工具配对、失败回执、旧修订、硬冲突、无候选及未满足目标，均不能走完成回退。
- [ ] **Step 2: 确认 RED。** `go test ./internal/agent/agents/travel -run 'TestTravelCompletedSelectionWithoutReplyFinishes|TestTravelIncompleteSelectionCannotFinish' -count=1`，阅读失败原因并留存。已有带 reply 的正常两次测试作为保护回归，不伪称它原本失败。
- [ ] **Step 3: 最小实现并 GREEN。** 在 completion_reply.go/native_tools.go 实现上述判断及绑定，仅使用 call_id、实际计划 ID、修订、校验与回执。无冲突成功后直接返回文本；有真实冲突仍进入原版修复流程。更新语义指纹。运行 Step 2 命令并确认全部 PASS。
- [ ] **Step 4: 完整路径与去重验证。** 新增 `TestTravelNormalPlanningTwoCalls`：有/无有效记忆、单程/往返/多城均用脚本模型断言 Main/Verifier 各一次、无主动历史读、每个规范物理查询实际供应方一次、只有必要写入。另测同查询自动项与独立 query 重叠只实际查询一次；使用现有缓存/singleflight，若失败先定位再改。保留 `TestTravelSourceFlowScenarios` 的 policy、replan、expiry、no_progress：真实重排必须交 Main、保留原计划与有效候选，不受两次硬限制。
- [ ] **Step 5: 自审并记录。** `go test ./internal/agent/agents/travel ./internal/agenttools/travel ./internal/service/trip -count=1`，并运行 PostgreSQL 工作流/恢复回归。写明重复模型尝试、缓存命中和恢复重放的区别；更新 tasks 后提交，建议 `fix: finish verified travel selections without redundant model turns`。

## Task 3: 实际模型验收、规格核对及交付

对应 OpenSpec tasks 2.4、3.1、3.2；覆盖全部规格场景。

**Files:** 创建 `backend/tests/integration/travel_interaction_model_test.go`；修改 `travel_evaluation_audit_test.go`、`travel_evaluation_contract_test.go`、`travel_source_model_test.go` 中共享的报告结构；创建 `docs/travel-normal-flow-verification.md` 和脱敏测量 JSON。

**Interfaces:** 沿用 `perfSample`、`perfInputLog`、ModelCallMeasurement 和现有固定供应方；为本场景增加主规划次数、政策前置次数、维护次数、未解释额外调用、主动历史读取和无进展重复计数。所有实际模型调用总数仍可与原记录逐次对账，不丢旧报告字段。

- [ ] **Step 1: 先测审计规则。** 扩展 `TestTravelEvaluationContract`：两次但只读历史并追问仍业务失败；真实 policy/replan 额外调用单列；缺原因的第三次及相同引用重复 select 记退化；缓存命中不能抵消多余工具尝试。加入报告分类前确认对应断言 RED，再最小实现并确认 GREEN。
- [ ] **Step 2: 真实验收。** 新测试仅在 `REQUIRE_TRAVEL_INTERACTION_MODEL_TEST=1` 时运行，输出路径由 `TRAVEL_INTERACTION_OUTPUT` 指定。截图原句、带长期记忆/缺省的正常往返、完整多城、普通独立查询每类三次；政策前置、需要真实重排和酒店无报价作为独立边界样本。使用真实模型及 PostgreSQL、固定且对模型无来源提示的供应方，不运行 Python 模型 A/B。
- [ ] **Step 3: 阅读实际结果。** 检查真实模型输入与回复：无问卷、无索取确认、无假起点或航班；完整路线/返程/必要住宿及选择真实保存，Main/Verify 两次路径比例和所有额外调用有证据。原句起点未知时以目的地建议与陈述缺口验收，不强造跨城行程。报告阶段耗时、全部 token、工具尝试、供应方调用和重复动作；保留失败。不能以某一次两次成功宣称普遍达标。
- [ ] **Step 4: 项目核验。** 从根目录加载 `scripts/load-env.ps1`；backend 内设置 `REQUIRE_DATABASE_TESTS=1` 后执行 `go test ./... -count=1 -timeout 180s`、`go vet ./...`、`go build -o ../.cache/bin/server-travel-normal-flow.exe ./cmd/server`。运行 `openspec validate prevent-travel-followup-questions --strict`，人工逐项核对 specs/design/tasks 与测试证据，明确 CLI 没有 verify，不声称运行过。
- [ ] **Step 5: 实际服务与归档。** 无活动任务时才部署通过验收的产物；记录监听 PID、go version -m 的提交/脏状态、8080 与前端代理健康、网页同句验收。先 archive 同步主规格并纳入工作分支，再按 finishing-a-development-branch 展示合并、PR、保留分支选项并执行用户选择。未解决行为失败或重要偏差时不归档。

## 计划审阅状态

技术复核已完成；用户已审阅并于 2026-10-25 选择单代理执行。推荐单代理：任务共享 flow、指纹及验收夹具，连续实施及整体自审开销较少。多子代理按独立任务实施并作规格/质量双重评审，成本更高；本次尚未授权分派。


