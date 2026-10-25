# 差旅测试与模型纠错 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用superpowers:executing-plans按任务单代理执行，不派子代理。每项采用有意义的RED→GREEN。

**Goal:** 消除测试来源提示污染，修复具体参数反馈和源部分收尾，证明模型实际收到纠错并以语义/效率验收。

**Architecture:** 沿现有TravelAgent→Eino内核→AgentTools→Service→Repository。独立benchmark供应方只替代外部数据；纠错提示和批次反馈仍走原模型/工具/journal/review链。

**Tech Stack:** 当前Go/Eino/pgx、真实PostgreSQL、现有React测试；不加依赖。

**Spec:** `openspec/changes/archive/2026-10-25-fix-travel-evaluation-and-correction/{proposal.md,design.md,tasks.md,specs/travel-source-fidelity/spec.md}`。产品行为只由这些OpenSpec文档定义。

## Global Constraints

- 用户2026-10-25明确要求测试与最终输出都将fixture当普通数据，修改校验反馈及之前审计问题；保留已有单代理方式，按已审阅的问题范围直接修复，不重复询问方案/执行方式。
- 保留五份原提示、辅助提示和工具schema的来源golden；授权的定向提示新增独立文件，并使fingerprint不兼容旧断点。
- 模型错误不得静默改用途、硬条件、来源和参数值。身份、权限、revision、attempt、批次预检、数据隔离不减弱。
- Main/Verifier/Summary和政策提取结构不变；仍为原8轮/Run100秒的benchmark口径，不增加循环/预算/网络重试。
- 不修改Python源项目，不导入旧数据，不把高德地点接口描述成可订报价；缺字段负例不通过伪造字段变绿。
- 真实模型仅从.env载入配置，不打印密钥；只做一次四场景冒烟，失败定位后再按必要范围补跑，全部保留。
- 先前调用审计及文档修正属于本次问题证据，保留，不覆盖旧round1–6。

## Review Focus

1. 只从最终回复删banner、政策辅助模型仍看到演示条款；Task1检查所有阶段实际messages及最终回复。
2. 同批合法read被误报自身字段错误，或预检第一项已经产生副作用；Task2检查根因/连带call_id与零handler副作用。
3. policy前置错误诱导business→visit；Task1用途验收拒绝此轨迹，Task2检查纠正指引与下一次真实模型输入。
4. 酒店未知报价与无关政策/硬冲突混在一起被过早终止；Task3正反例证明分类精确。
5. fixture报价让真实高德能力被夸大，或仅看仍有Train放过候选被替换；Task1/3独立记录fixture模式并断言原ref完全不变。

## Task 1：正常数据夹具与有效验收

**OpenSpec:** 1.1/1.2；规划测试无来源提示、用途与重复动作参与验收；design D1/D5。

**Files:** 修改`backend/tests/integration/travel_source_model_test.go`；新增同包`travel_evaluation_fixtures_test.go`（数据/模型输入记录/验收辅助，若大小过大将记录器单独拆同包文件）；复用现有travelFixture，不建新数据库框架。

**Interfaces:** perfProvider继续实现trip.QueryClient；新增newPerfProvider(now func()time.Time)；perfPolicy.Search返回普通证据。录制包装实现Eino ToolCallingChatModel，记录完整脱敏messages/tools且仍只调用内部Generate一次。验收输出功能与效率两个独立结果。

- [x] 先写`TestTravelEvaluationContract`综合用例：旧perfPolicy及现有demo供应方导致“演示/IsDemo”断言失败；business→visit即使数量齐全仍失败；连续相同selection_refs被计为重复，纯解释变化也不能绕过；14→16修改后原候选ref不同应失败。首轮临时只让新constructor编译并复用旧demo，以观察实际失败，而不以编译错误充当RED。
- [x] 最小替换为独立完整报价fixture：车次/库存、入住退房全价、天气及政策引用语义正常，不含演示名称/ID/正文；报告fixture标签与模型材料分离。缺价负例单独构造。
- [x] 去除用户测试query中的演示说明及“出差但无需政策”的冲突指令；保留原四类请求、三段/两晚/两活动/硬预算/局部修改要求。一次捕获未来日期填充所有请求和种子数据，报告留实际prompt。
- [x] 录制实际模型输入并脱敏；验收schema字段、完整路线日期、活动时间、purpose、refs，记录root/collateral拒绝和重复select。传统Success不再只由数量得出，另给efficiency_success。
- [x] 运行`go test ./tests/integration -run '^TestTravelEvaluationContract$' -count=1`，Expected: GREEN；不连接真实模型。更新任务1及实施日志，提交该独立交付。

## Task 2：参数指引、批次归因与提示

**OpenSpec:** 2.1/2.2；可修正参数错误、错误实际返回模型；design D2/D3。

**Files:** 修改`backend/internal/agenttools/travel/{registry,schema_errors}.go`及`source_batch_test.go`；修改Travel `{batch_gate,flow,prompt,agent}.go`及`batch_gate_test.go`；新增`prompts/correction.txt`。

**Interfaces:** PreparedBatch新增内部FailedCallID；Registry.Preflight仍返回原PreparedBatch/error。POLICY_OBSERVATION_REQUIRED与misplaced_field的Details包含固定field、correct_path/next_action，保留字段值由模型重发。批次输出为根因原错误及其他BATCH_ABORTED，均executed=false、failed_call_id配对。原journal和resultSummary接口不变。

- [x] 在TestTravelSourceBatch补政策前置与错位路径综合断言，在真实Eino批次测试补“下一次Generate收到根因、正确路径/下一步、业务用途保持提示、两个配对结果”；先运行至RED。副作用次数为0，错误不echo任意用户值。
- [x] 最小实现明确反馈和FailedCallID；前置错误不能建议visit。对已知错位字段给出准确任务下标路径；没有明确归因的批次冲突仍作为批次错误，不伪造根因。
- [x] 补充correction.txt接入beforeModel，校验原文基线不变及补充实际存在；将内容和flow/batch版本纳入fingerprint。原版schema名称和阶段授权不变，不额外引入模型修复调用。
- [x] 运行`go test ./internal/agenttools/travel ./internal/agent/agents/travel -count=1`，Expected: GREEN，旧配对、零副作用、恢复、未知工具等回归不退化。更新任务2和日志并提交。

## Task 3：源能力收尾与真实模型复核

**OpenSpec:** 3.1/3.2/3.3；已知缺口部分收尾、测量口径及验收要求；design D4/D5。

**Files:** 修改`backend/internal/service/trip/patches.go`；扩展`source_rules_test.go`及Travel源流程综合测试；更新README和docs/travel-evaluation-correction-verification.md，维护本change的tasks。

**Interfaces:** finishSourcePlan沿原TripPlan/Validation返回StopReason，不加Repository或迁移接口。酒店policy rule.id证据缺失与hotel_place报价缺失对应时为provider_capability_missing；现有StopPlanning安全点负责最终渲染，不另调模型。

- [x] 先写综合RED：已选无报价酒店＋其hotel_*政策证据缺失应停止；包含无关policy_unverified、train_pending或硬冲突不得误判。真实Eino脚本断言该结果之后无下一次付费Generate、仍保留所存成果。
- [x] 按Python _capability_gap_only对齐最小分类；不改无进展阈值、用户硬约束或工具权限。
- [x] 运行相关模块检查到GREEN；再`REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s`、vet、server build，以及前端已有test/build。Linux race按CI实际结果报告。
- [x] opt-in真实模型运行四场景各1次（通过环境变量选择repeats=1，默认历史3次模式仍可显式开启），输出新独立报告。正常完整证据应语义正确且零拒绝/零重复；超出正常路径的调用逐项解释，不靠最终落库隐藏。失败保留并先诊断，必要复跑不能拼成一次全绿。
- [x] 人工核对所有spec场景及实际messages/返回，运行`openspec validate fix-travel-evaluation-and-correction --strict`，只有全部满足才标任务完成/归档。源基线不改，公开披露实际提示补充及fixture能力边界。

## Pre-flight

- Task1的fixture和验收由Task3真实模型入口消费，模型接口不变。
- Task2的PreparedBatch.FailedCallID仅执行内部使用，journal仍存原操作身份及同一结构观察；Task1从原事件计数即可。
- Task2新增提示使Task3恢复指纹变化有据可核对，业务快照/数据库版本不变。
- Task3 StopReason由现有NativeTripView和sourceFlow消费，不改变AgentManager生命周期。

## 执行授权与记录

用户已审阅前文问题清单并明确说“校验反馈也改，你之前总结的问题就改”，继续采用已选单代理。方案按上述授权范围直接实施，不增加待选架构或新的产品行为；仅在发现超范围的重要选择时再询问。私有执行日志为.cache/execution/travel-evaluation-correction/progress.md，实施后按真实证据更新OpenSpec tasks。

## 完成证据

Task1：36dbf17；Task2：016ad3d；Task3：576e1d6及ca25daa。RED/GREEN日志保留在.cache/execution/travel-evaluation-correction；两次完整模型原始报告与人工核对矩阵见docs/travel-evaluation-correction-verification.md。run1失败未删，夹具物理请求及重复计数自审修正均有回归；run2四场景整体4/4。ca25daa的LinuxCI40155219953 race/真实PG、vet/build及前端全部成功。旧deadline测试改为比较继承的绝对deadline，不改产品预算。原Python93文件SHA一致。
