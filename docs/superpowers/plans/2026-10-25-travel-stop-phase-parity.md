# 差旅停止优先级与状态转换 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用superpowers:executing-plans按已选单代理方式实施，不派子代理；按任务执行RED→GREEN并维护OpenSpec tasks。

**Goal:** 修复两项确认的Python迁移差异，并向当前默认分支提交可审阅PR。

**Architecture:** 业务停止原因仍归service/trip；执行状态归TravelAgent内核，平台Run/Repository边界不变。使用分支codex/travel-stop-phase-parity，基线aee1bb4，PR目标feat/agent-platform-mvp。

**Tech Stack:** 现有Go/Eino/pgx/PostgreSQL，无新增依赖。

**Spec:** openspec/changes/fix-travel-stop-phase-parity/{proposal.md,design.md,tasks.md,specs/travel-source-fidelity/spec.md}。本计划只展开执行步骤。

## Global Constraints

- 不改提示词、模型参数、预算、Schema或业务硬约束，不追加真实模型付费评测。
- Python源项目只读，原提示golden不变；新执行语义计入指纹，历史保留。
- 单代理、少量有意义的综合回归；使用现有Eino和PostgreSQL底座。

## Review Focus

1. 政策+allow_repair=false及酒店+allow_repair=false仍应按原优先级收尾。
2. 一般硬冲突与硬日期冲突不能混成一个优先级，停止原因不能绕过validation。
3. 批次拒绝后的repair_required应允许合法纠正，不能把正常第二轮误拒绝。
4. 终态不能重开工具，finalize重复调用保持幂等，无额外模型或业务副作用。
5. 快照装载不误认为活跃跳转；已接受决策及恢复仍不重复写入。

## Task 1：Service停止优先级（OpenSpec 1.1/1.2）

**Files:** 修改backend/internal/service/trip/patches.go；新增stop_priority_test.go；复用source_rules_test.go/capability_gap_test.go。
**Interface:** finishSourcePlan(*model.TripPlan, model.TripCommand)签名不变，不新增Repository接口。

- [ ] 写TestSourceStopPriority组合表，明确政策+false→policy_unverified、酒店缺口+false→provider_capability_missing、硬日期保留及一般硬冲突的原分支结果，另含暂停/取消和有效提交；确认旧代码实际优先级断言失败而非编译失败。
- [ ] 按Python _finish对应互斥分支最小修复；现有Validation/候选/用户条件不改。
- [ ] 执行go test ./internal/service/trip -count=1，Expected PASS；更新tasks和执行日志，提交可独立审阅改动。

## Task 2：执行状态及钩子（OpenSpec 2.1/2.2）

**Files:** 新增backend/internal/agent/agents/travel/phase.go与phase_test.go；修改flow_state.go、flow.go、protocol.go、presentation.go、agent.go及必要已有脚本测试。
**Interfaces:** FlowState.transition(next string) error、stop(reason string) error、summaryOnly() bool；ObserveReview如需传递错误改为返回error并更新所有调用者。所有阶段变化只有统一入口可写Phase，快照反序列化仅验证已知阶段。

- [ ] 写TestTravelPhaseTransitions：对原表全部允许/拒绝边做一份综合断言，检查错误码/from/to及状态不变，同状态幂等、未知阶段、首个停止原因保留。先编译出最小空入口后确认行为RED。
- [ ] 实现原9阶段表，接入reserve/modelObserved/beforeModel/afterBatch/无进展/协议/finalize，补齐合法中间阶段；错误批次转repair_required，合法修复可再执行。
- [ ] 用现有真实Eino脚本和恢复测试验证正常两轮、拒绝后修复、协议重生成和部分结果；必要时补一个终态工具请求零副作用边界，不放宽状态表来迁就旧测试。
- [ ] 更新指纹为新的stop/phase版本；执行go test ./internal/agent/agents/travel ./internal/agenttools/travel -count=1以及相关PostgreSQL恢复集成，Expected PASS；更新tasks并提交。

## Task 3：整体验证与PR（OpenSpec 3.1/3.2）

- [ ] REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s、go vet ./...、go build ./cmd/server；推送修复分支后核实LinuxCI的race/真实PG及前端既有检查。失败先定位，不重复付费评测。
- [ ] 整体自审并人工核对本spec全部场景，openspec validate fix-travel-stop-phase-parity --strict；记录实际结果及未覆盖边界。
- [ ] 所有产品任务/验证通过后执行OpenSpec archive，同步主规格，计划链接转归档，提交文档；创建PR到feat/agent-platform-mvp并attach_artifact，报告链接，不合并。

## 执行记录

用户已选择“先修复刚才发现的停止优先级和状态机差异，再向当前默认分支提PR”，沿用单代理。实施前按用户AGENTS.md第三阶段交付本计划审阅；未审阅前只写规划文档，不修改产品代码。私有执行日志使用.cache/execution/travel-stop-phase-parity/。
