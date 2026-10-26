# 差旅正常调用流程验收（2026-10-26）

历史记录：本报告采用当时“缺起点保留未知”的旧验收规则。用户后续明确缺省重庆，该行为和最新测试结果以[缺条件直接安排验收](travel-planning-defaults-verification.md)为准；下方18样本及部署记录保留为历史证据。

基线为用户合并后的 main `2a4b3c6`，工作分支 `codex/travel-normal-flow`。按用户选择由当前代理连续实施、整体自审，未派子代理。正式依据为 OpenSpec 变更 `prevent-travel-followup-questions` 与 [实施计划](superpowers/plans/2026-10-25-travel-normal-flow.md)。

当时版本的完整真实模型＋PostgreSQL验收 **18/18 自动与手工核对通过**；全量 Go/PostgreSQL 测试、vet、build 通过。最终回复零追问，主动历史读取、重复选择、审计未解释额外调用均为零。模型仍会产生错误提议，拒绝与修复成本均计入总数。

| 场景 | 各次实际模型调用 | 结果 |
|---|---|---|
| 截图原句／未知起点 | 1, 5, 2 | 3/3 |
| 长期记忆＋缺省往返 | 2, 2, 2 | 3/3 |
| 往返＋两项硬活动 | 5, 7, 4 | 3/3 |
| 三城＋两晚住宿 | 2, 2, 2 | 3/3 |
| 独立高铁＋天气查询 | 2, 2, 2 | 3/3 |
| 企业政策前置 | 4 | 1/1 |
| 无直达、经南京中转＋硬一晚 | 4 | 1/1 |
| 酒店硬预算、报价缺失 | 2 | 1/1 |

资料完整的九个规划样本中，**6/9（66.7%）恰好两次**；加上三个独立查询为9/12。未知起点样本单列，不伪造跨城交通；酒店报价缺失按部分结果验收。政策样本为两次前置（Main发工具、政策提取）＋两次规划，全部四次照计。重排样本保留10月29日返程，将获准调整的去程改至10月28日，经南京同日中转，杭州10月28日至29日硬一晚。

本批全部 **52** 次模型请求：规划/查询 **50**、政策 **2**，维护和压缩均为0。实际输入 **455,171**、输出 **29,538**，合计 **484,709 tokens**，逐次usage齐全。样本数小，不能外推为全场景可靠性或固定两次承诺。

额外调用手工核对：未知起点的一次样本为5次，含两次整响应交互拒绝及两个参数错误；另一次为2次，含一次交互纠错。往返硬活动三次为5/7/4次，分别修复漏活动或活动归属，使用真实request_replan、保留候选，未冒充正常两次路径。有效记忆与多城各三次均为两次。

## 改动及根因

- 截图最初运行旧产物 `f1db682`，旧提示明确要求缺条件询问；后续源码虽恢复原版，实测仍会主动读历史或追问。因此修复运行版本，也增加实际规则和执行保护。冻结的五份 Python 提示、原生schema与0.3/8192/thinking-disabled生成配置保持原样；独立补充 `no_followup.txt`、运行保护及语义指纹有明确变化。
- 有效记忆直接注入 Main/Verify；明确新规划不开放历史read，已持完整计划的重排Main也不重复读。明确历史查询/修改保留。每三条新增确认有效行程的低频归纳保留，未达阈值零分析；不会添加意图分类模型。
- Verifier同次选择并绑定实际回执。遗漏reply只在明确allow_repair=false且完整匹配回执、校验完整时确定性绑定；不能仅凭字段complete提前跳过后续语义修复。
- 自动train项携带无关Guests、hotel项携带无关Passengers，导致与独立query物理键不同。只移除这些无关参数，复用既有cache/singleflight；具体任务/修订/证据及权限绑定保持。
- 明确索取用户信息时整次响应拒绝，工具不执行；沿用两次额外再生成预算，不删改句子掩盖。拒绝全文和原因保存在travel.model_rejected。泛化缺口提示改陈述；硬活动/整体路线错误提供Main交接，待交接阶段只接受request_replan。
- 手工核对曾发现自动断言漏查住宿缺夜，补齐planned/actual退房至下一程的阻断检查；旧酒店查询与退房条件不再让已取消住宿的中转任务失败。硬晚数首次结构化保存后在本请求内保留，Main也不能把硬一晚改成硬两晚。具体字段错误不再只报泛化信息。

生产记录链路另经真正Manager→PostgreSQL验证：原先白名单丢弃模型测量/拒绝事件，首次网页测试只留下两条已接受决策，实际调用总数不可从缺失事件推为零。现补齐两类事件，当前尝试和旅行类型限制保持，拒绝全文最大256KB；不会作为普通聊天输出显示。此后仅改审计持久化，未改18样本的决策代码。

## 覆盖矩阵与实际验证

| 需求/任务 | 证据 |
|---|---|
| Main/Verify/Summary 无追问规则；空/有效/撤回记忆与同批更新 | TestTravelInteractionInstructions、TestTravelInjectedMemoryAcrossRoles、TestTravelMemoryIsolation |
| 三条新增阈值、跨批覆盖不重复分析 | auxiliary_test.go 的维护/覆盖回归 |
| 正常两次、缺reply明确结束、重复物理查询、恢复重放 | TestTravelNormalPlanningTwoCalls六变体、TestCompletedExplicitStopReply、TestTravelEquivalentPhysicalQueriesIgnoreOtherDomainParty及原有恢复回归 |
| 不接受问卷、预算有界、明确历史操作保留 | TestTravelQuestioningResponseIsNotAccepted、TestTravelInteractionRequestBoundaries、TestTravelFreshPlanningCannotReadHistory、工具profile/preflight回归 |
| replan权限、字段反馈、过夜、已取消酒店、新请求硬晚数 | replan_feedback_test.go、condition_feedback_test.go、stay_coverage_test.go、inspect_scope_test.go、hard_stay_test.go |
| 真实Manager审计事件不丢失及旅行/attempt/大小边界 | TestTravelModelAuditEventsPersistThroughManager、TestTravelModelAuditEventGuards |
| 真实18样本的语义、数量、成本、全部额外调用 | TestTravelInteractionModel，原始输入/响应/事件/候选/测量保存在完整本地JSON |

```text
REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout 180s
go vet ./...
go build -o ../.cache/bin/server-travel-normal-flow.exe ./cmd/server
REQUIRE_TRAVEL_INTERACTION_MODEL_TEST=1 TRAVEL_INTERACTION_REPEATS=3 go test ./tests/integration -run ^TestTravelInteractionModel$ -v -count=1 -timeout 20m
openspec validate prevent-travel-followup-questions --strict
```

全量日志：`.cache/execution/travel-normal-flow/backend-tests-audited.log`、`vet-audited.log`；实际模型日志：`task3-real-final2-1026.log`。脚本模型测试原先硬编码2026年10月25日，跨日后已过期；仅将相关脚本夹具改为2099年未来日期，真实模型场景继续用当前上海时区相对日期。未改生产时钟或放宽过去日期校验。

本机 CLI 没有 OpenSpec verify 命令。执行的是 strict validate，另人工逐项核对 specs/design/tasks/代码/测试与上表；没有声称运行 CLI verify。

整体代码自审：核对工具授权与scope、无副作用预检、回执匹配和持久化重放、快照恢复、查询键差异、硬/软住宿界线、原版提示/schema/生成配置、报告计数及失败保留，未留下阻断发现。本次按单代理约定自审，未冒充独立评审。

## 证据、失败记录与限制

[可提交的脱敏测量及历史结果索引](reports/travel-normal-flow-2026-10-26.json)。完整最终本地报告：`.cache/reports/travel-interaction-2026-10-26-final2.json`，包含逐次真实模型输入、工具、全部事件及完整拒绝内容。此前probe/final/focused报告均保留，没有只保存成功样本。其中2026-10-25 final4的自动18/18被手工住宿漏夜检查否决，不能作为通过证据；10月26日早先16/18的失败也保留。原句在10月25日映射明天为26日，本次10月26日重测映射为27日。

关键取舍：广泛complete无reply回退已撤回，避免跳过必要活动修复；维护夹具改为使用真实PrepareNativeMemory覆盖并集，没有虚构生产归纳缺陷；只读Inspect过滤已失效酒店范围而不重写历史；硬晚数保护来自已结构化user/context事实，不额外解析自然语言。全部RED/GREEN与裁决见本地 `.cache/execution/travel-normal-flow/progress.md`。

供应商及企业政策为未向模型披露来源的固定夹具，不能证明线上车次库存、酒店报价或在线RAG质量；未运行Python模型A/B，Python源码只读。交互/历史权限用有限语言规则，有边界而非通用语义保证；已通过的小样本不保证后续每次模型都无错误。效率通过与两次达标单列，真实修复不等于正常两次。

## 本地服务交付

2026-10-26 已在确认无活动任务后部署干净提交 `cfab19f5535579efc2d1a3056e4137fb0b7db1f8`，`go version -m` 显示 vcs.modified=false。后端 PID 28344，产物 `.cache/bin/server-travel-normal-flow-audited.exe`，8080 ready；前端 `http://127.0.0.1:5173` 返回200，标题为小豆包。此机器 IPv6 localhost:5173 属于其他应用，未停止该应用，访问须使用上述 IPv4 地址。后续归档提交仅包含文档与规格，不改变运行代码。

经过5173代理、携带真实浏览器Origin、独立测试账户执行截图原句，run `715f9fc6-4aa6-462b-bafa-8507f2251d24` succeeded，13.17秒。最终回复给出上海目的地建议与陈述缺口，起点仍未知，无问卷、无索取确认、无虚构已核实交通/住宿。数据库保存 **3次实际模型调用、2次整响应拒绝、1条接受决策、0次read**；输入24,213、输出2,416，合计26,629 tokens。两次拒绝均含等待用户补充起点的表述，完整响应保留且未执行其中工具。该样本不是资料完整的两次路径，且没有可执行的跨城交通查询；不把三次Main生成误报为Main/Verify各一次。

本地证据：`.cache/reports/travel-deployment-2026-10-26.json`、`travel-no-followup-2026-10-26-audited.json`、`travel-no-followup-2026-10-26-audit-events.json`。首次旧审计链网页样本仍保留为 `travel-no-followup-2026-10-26-final.json`，不能据其中缺失模型事件声称零次。首启曾因状态检查用PGOPTIONS只读选项被子进程继承而迁移失败；清除该检查选项后正常启动，未修改迁移逻辑。

本次验证覆盖网页代理API路径，未冒充手动点击浏览器的UI验收。OpenSpec已于2026-10-26归档到 `openspec/changes/archive/2026-10-26-prevent-travel-followup-questions`，同步travel-execution、travel-memory、travel-planning、travel-source-fidelity四份主规格。归档前唯一未勾项为部署/归档本身，依据实际成功结果已补记；无未完成产品任务。分支整合由用户选择，当前未合并或推送。
