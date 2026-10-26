# Main一次与总结直接重排 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans；用户本次已选择单代理，连续TDD与整体自审，不派子代理。

**Goal:** Main只首次规划，政策辅助最多一次Main续规划；重排由总结直接改当前计划，不再发起第二个Main。

**Architecture:** 独立planning_summary可信角色/profile；程序handoff及限额、硬条件及事务保护；协议再生成也使用总结真实输入和工具。保留冻结Python提示/schema基线，授权扩展单独记录。

**Tech Stack:** Go、Eino、PostgreSQL。

**Spec:** openspec/changes/fuse-travel-summary-replanning/specs/、design.md、tasks.md。产品要求只由这些文件定义。

## Global Constraints
- Main首次一次，真实政策观察例外一次；不能用只改阶段日志伪装合并。
- 当前请求计划范围、硬日期/预算/晚数/活动、用户及attempt隔离、来源元数据、原有预算不放宽。
- 记忆直接注入、默认重庆、不追问、独立查询范围保持；所有失败与实际模型调用均计数。

## Review Focus
- 总结拿到Main schema后创建替代计划/写记忆/跨ID：Task1负例必须零副作用。
- 路线插入中转使硬条件随任务丢失或活动换ID消失：Task1跨任务硬事实保护。
- Main参数被拒/追问再生成仍调Main：Task2包括真实输入、工具集与计量。
- 政策观察被反复当Main重入许可：Task2持久化一次例外，负政策/恢复不复位。
- 缓存重放hash用旧Main工具、日志却写总结：Task2/3 journal和真实输入对账。

## Task 1: 规划总结权限（OpenSpec 2.1）
Files：agenttools/travel新增planning_summary.go及测试；registry.go、source_contracts.go、normalize.go、replan_feedback.go；service/trip新增summary_requirements.go及测试、patches.go。
Interfaces：ProfileRequest.Role与TripDecisionContext.Role新增可信planning_summary；SourceProfile新增运行planning_summary/planning_recovery；既有工具JSON与HTTP不变。
- [x] 先写测试：总结amend完整route_tasks可通过，旧verifier仍拒；已有计划不能propose/其他ID/pause/cancel/write_memory/request_replan，初提议恢复不能任意amend；返回schema不污染SourceProfile(main/review)；无授权均零业务写入。
- [x] service Preview测试先RED：合法插南京且软日期可变、固定返程/硬一晚保留；硬日期、硬预算、硬活动丢失或改时段、删除带硬条件的旧任务均拒，原输入不变。
- [x] 最小实现角色和schema克隆筛选，预检范围与service硬事实保护，跑agenttools/service相关回归及冻结SHA全部GREEN，记任务证据。

## Task 2: 自动handoff与Main调用预算（OpenSpec 2.2）
Files：flow_state.go、flow.go、compaction_agent.go、prompt.go、新planning_summary.txt、no_followup.txt、interaction_protocol.go、protocol.go、journal.go、stage.go、agent.go及测试。
Interfaces：FlowState.MainUsed/PolicyContinuationUsed序列化；内部阶段agent:trip_summary；业务、查询作业及现有checkpoint接口不变。
- [x] 先写行为RED：Main完成/工具拒绝/request_replan结果均下一步planning_summary；真实政策仅解锁一次Main；独立query仍summary；序列化后标志与行为保持。
- [x] 新规划总结提示覆盖局部Verifier交Main规则，整个角色实际输入/工具集与阶段一致；generateSourceTurn中Main坏响应后转总结，原2次额外再生成预算保持。
- [x] journal按最终真实input＋实际工具profile接受/恢复；脚本模型捕获Main1→总结repair→总结select三次完整路径，政策Main2，参数拒绝与追问恢复Main1，恢复重放零重复模型/写入。更新明确旧replan场景预期，不删除其成果/修复断言。
- [x] 运行角色、协议、来源、journal、流程、PG恢复回归GREEN；更新指纹包含新提示及运行profile和语义版本。

## Task 3: 实际验收与交付（OpenSpec 2.3、3.1、3.2）
Files：travel_interaction_model_test.go、travel_interaction_audit_test.go、travel_defaults_audit_test.go；新验收报告及脱敏JSON。
- [x] 审计先RED：正常Main不得再次调用，真实路由amend记有效重排，独立query/维护/政策仍计总数；重排用现有固定无直达工具，重复三次，判Main1/总结2且完整满足固定返程硬一晚。
- [x] 真实模型完整重复：原句/记忆/硬活动/多城/查询每类三次，明确单程、政策、酒店缺报价对照；检查角色、无追问、去返程/活动/住宿及全部成本，所有失败保留。
- [x] REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s；go vet ./...；go build；整体自审与openspec validate fuse-travel-summary-replanning --strict，CLI无verify则人工规格矩阵核对。
- [ ] 干净提交构建，无活动任务后替换本地服务；网页原句和实际调用核对；合格后archive同步规格，保留当前分支待用户选择整合，不自动合并或推送。
