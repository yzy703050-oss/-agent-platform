# 差旅缺条件自动补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用superpowers:executing-plans；用户在本次任务已选择单代理，连续实施和整体自审，不派子代理。

**Goal:** 按用户明确要求缺起点默认重庆，自行安排去返程和必要酒店，不将条件不足交回用户。

**Architecture:** 独立版本化提示覆盖旧未知起点规则，实际上下文注入默认值并保留有效记忆及本轮要求优先；正常Main→程序工具→Verify。无新增模型或底层强制改城市。

**Tech Stack:** Go、Eino、PostgreSQL。

**Spec:** openspec/changes/archive/2026-10-26-complete-travel-planning-defaults/specs/travel-planning/spec.md、design.md、tasks.md；本计划只展开实施。2026-10-26已同步主规格并归档。

## Global Constraints

- 用户明确条件>有效注入记忆>授权软默认；来源不能假标为user；单程/不住宿不得被默认覆盖。
- 现有政策前置、阶段权限、预算及原版提示基线不变；全部实际重试计数。
- 沿用codex/travel-normal-flow，不改无关add-general-web-search变更。

## Review Focus

- 原句只有Main且needs_input停止：Task2须完整路由和Verify，禁止沿用旧partial判据。
- 记忆上海与默认重庆冲突：Task1输入隔离、Task2真实记忆对照。
- 显式单程不住宿被默认覆盖：Task2真实对照。
- 长途交通占满默认天数：Task2检查保留活动、逐晚住宿及返程衔接。
- 提问被拦截却报告零提问：Task2单列模型拒绝次数与最终无追问。

## Task 1: 明确默认并消除冲突（OpenSpec 2.1）

Files：interaction_test.go、prompts/no_followup.txt、interaction_protocol.go、flow.go。
Interfaces：beforeModel保持原签名，当前上下文追加planning_defaults；interactionInstruction与interactionRegenerationInstruction继续计入既有fingerprint。

- [x] 先写TestTravelPlanningDefaultsAcrossRoles，在真实Main/Verify及Summary输入中断言默认重庆、人数1、高铁二等座、往返、相对日期与来源优先级；有效记忆不被写成默认资料；首次原模型两次路径保持。
- [x] `go test ./internal/agent/agents/travel -run TestTravelPlanningDefaultsAcrossRoles -count=1`，确认缺默认字段导致RED。
- [x] 最小修改上述三个生产文件，去除独立补充和重试指令旧“未知起点不补城”规则，补齐具体默认。
- [x] 跑默认输入/已有交互/来源SHA及角色测试GREEN，记录证据并更新tasks。后续实际失败补充original_query与apply_only_if_missing上下文输入RED/GREEN；完整住宿任务条件与新规划完整展示单独明确，见design决定5。

## Task 2: 完整规划验收（OpenSpec 2.2）

Files：travel_interaction_model_test.go及必要的验收断言测试；原perf固定夹具不改，通过interactionProvider局部提供长途时刻、多晚价格。
Interfaces：保留interactionCase/interactionSample及报告字段；sparse沿用场景名但改为默认重庆完整规划验收，旧报告保持原判据与历史结果。

- [x] 加验收断言测试：缺返程/酒店/活动/Verify、仍needs_input、伪user起点不通过；重庆完整往返与合理默认可通过。确认旧partial样本不满足新验收；新增末次局部reply遮蔽整趟初始结果的RED/GREEN。audit-red首次undefined仅编译失误，不算行为RED；真正行为失败见audit-behavior-red.log。
- [x] 原句重复3次；memory_defaults重复3次；明确上海单程不住宿对照一次；其余既有场景回归。检查真实保存查询与选择，报告每次调用/拒绝/耗时/token，保存所有失败。最终19/19，原句2/2/2，零生成追问；run1、run2失败均保留。
- [x] 在真实模型输入与最终计划中核对日期/路线/交通占用/住宿和原需求，不能以脚本数量断言替代手工核对。新增长途早班返程与默认上午活动矛盾断言RED/GREEN；提示不在默认长途返程日补软游玩，最终夜间抵达→中间日游览→返程日纯交通。硬活动对照保持；重排28日同日中转、29日固定返程与杭州硬一晚。

## Task 3: 核验部署与归档（OpenSpec 2.3、3.1）

- [x] REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s；go vet ./...；go build；自审实际diff、测试及规格矩阵，openspec validate complete-travel-planning-defaults --strict。CLI无verify，人工核对并明确报告。最终PG47.252秒，全部命令退出0，无阻断发现。
- [x] 提交干净代码add6904后构建vcs.modified=false，确认无活动任务、核对当前服务身份，再隐藏启动新版本PID36356；8080 ready、正确IPv4前端代理5173 HTTP200。
- [x] 网页同句真实查询验收，记录已授权默认重庆、去返程/酒店、真实调用及报价/库存边界；成功后archive同步主规格，保留分支待用户选择整合。原句run79370487-21a6-49c9-b711-e7730cbfd0ee成功10.64秒，3次实际调用含真实活动修复，0追问/0read；完整方案显示，demo来源如实披露。2026-10-26 archive已完成，后续仅文档提交，不改部署代码。分支整合待用户选择。
