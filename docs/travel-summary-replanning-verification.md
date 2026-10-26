# Main一次、总结直接重排验收（2026-10-26）

按用户要求，Main只首次生成完整规划与查询地图；程序执行工具后直接handoff规划总结Agent。总结发现路线问题时，在当前这次调用直接amend当前行程并提交受影响查询；新候选回来后仍由总结选择与回复。只有真实企业政策前置允许Main再调用一次。参数、协议和交互纠错也交总结，不借错误重进Main。

用户已选择单代理执行。正式需求为OpenSpec `fuse-travel-summary-replanning`，详细步骤见[实施计划](superpowers/plans/2026-10-26-travel-summary-replanning.md)。本次运行权限扩展与冻结Python基线分开记录；Python源码实际有request_replan→Main，本次按用户最新授权去掉这个运行回边。

## 真实模型结果

最终完整回归结果及逐样本调用、用时、token、保存行程见[脱敏数据](reports/travel-summary-replanning-2026-10-26.json)。所有历史失败保留在原始报告，未覆盖、未从总样本中删掉。

最终21/21通过。全部非政策样本Main=1，政策样本Main=2；零追问、零交互再生成、零主动历史读取、零当前计划读取、零重复选择、零无原因额外调用。普通完整规划13例中11例为2次（84.6%），2例为3次；单独酒店缺报价对照也为2次。不是所有场景固定两次。

| 场景 | 实际模型调用 | Main | 规划总结 | 结果 |
|---|---|---|---|---|
| 截图原句，默认重庆 | 2、2、2 | 各1 | 各1 | 3/3 |
| 明确上海单程、2人、一等座、不住宿 | 2 | 1 | 1 | 1/1 |
| 上海起点记忆、明天去后天返 | 2、3、2 | 各1 | 1、2、1 | 3/3 |
| 往返、西湖下午及返程日上午硬活动 | 2、3、2 | 各1 | 1、2、1 | 3/3 |
| 三城两晚、两项14:00活动 | 2、2、2 | 各1 | 各1 | 3/3 |
| 仅查车次与天气 | 2、2、2 | 各1 | 0（独立查询Summary各1） | 3/3 |
| 企业政策前置 | 5 | 2 | 2，另政策提取1 | 1/1 |
| 无直达、南京中转、固定返程硬一晚 | 3、3、3 | 各1 | 各2 | 3/3 |
| 酒店缺报价/房态 | 2 | 1 | 1 | 1/1，正确保留缺口 |

南京中转均实际保存：10-28上海09:00→南京11:00；南京14:00→杭州16:00；杭州10-28入住、10-29退房一晚；10-29杭州14:00→上海16:00。耗时13.26、13.25、12.18秒。调用2直接修路线，调用3选择并回复，没有request_replan→Main。

额外调用逐例核对：记忆例2先修正软活动后选择；硬活动往返例2因condition_strengths参数枚举错误再生成；政策例中总结多标passengers为soft被既有硬约束拒绝，修正后完成。三例均由总结继续，未重进Main；政策实际5次全部计入，不标成正常4次。

最终批次共537609 token，逐调用usage完整，未折算无法核实的金额。小样本与固定工具不能证明普遍成功率；中间失败也保留在下文及脱敏数据中。

真实模型和真实PostgreSQL，使用固定供应方与政策夹具；无直达由测试供应方返回no_results，模型看到实际工具结果后重排。不能据此判断线上真实库存或RAG质量。本地网页供应方另为demo，回复明确说明演示数据。

## 原始记录及修复过程

原始完整输入、工具和响应保存在本地 `.cache/reports/travel-summary-2026-10-26-*.json`，执行记录见 `.cache/execution/travel-summary/progress.md`。可提交的脱敏JSON保留每轮原验收通过数、全部失败原因及最终完整行程。

- `run1`：19/21。中转一次选同刻发车导致额外修复，另一次错误挪用目的地ID后重复修改耗尽上下文；已补任务归属保护与联合时刻比较指引。
- `replan2`：3/3，中转都是Main1＋总结2。
- `final`：原计数断言20/21；唯一失败实际完成了行程，但首次Main日期参数错误被拒后用了Main1＋总结3，固定3次断言过严。保留原失败，计数断言仅按真实根拒绝/交互恢复容纳对应有界纠错，无原因额外调用及非政策Main重入仍失败。
- `final2`：20/21，中转一例Main漏标用户允许调整的任务出发日期，继承成hard，总结修改被正确拒绝。补首次Main全局start_date与任务departure_date均需明确soft的提示，保留硬条件保护。
- `replan3`：3/3，实际3、3、4次，Main均1。额外一次有真实参数纠错，完整保留。

## 实现与保护

- 新可信角色 `planning_summary`，已有计划仅amend/select当前授权ID，无计划时才可恢复propose；不开放write_memory或request_replan。
- MainUsed与PolicyContinuationUsed持久化。首次Main追问/无效协议后，重新生成真正换成总结提示与工具，实际stage和journal哈希与模型输入一致，不只是改日志名称。
- 总结可以改完整路线，不能改用户硬日期、预算、晚数或删除/移动硬活动；目的地任务保留原ID，新增中转不挪用旧ID，预检与事务继续保护修订、证据、用户和attempt。
- 默认重庆、用户及注入记忆优先、完整往返/住宿/活动、禁止追问保持；不主动读历史补资料。查询、选择回执与handoff本身不增加模型调用。
- 原模型、规划、查询、上下文和无进展停止预算保持。旧断点按新指纹拒绝不兼容恢复。

## TDD、代码自审与规格核对

执行了有意义的行为RED→GREEN：新增权限、硬条件、角色转换、追问与参数恢复、实际工具录制、停止后权限、调用审计。编译错误不计行为RED。证据日志均在 `.cache/execution/travel-summary/`。

`REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout 180s`、`go vet ./...`、服务构建均退出0。PG恢复测试覆盖已提交后丢失观察、跨attempt恢复：恢复只调用总结一次，Main零次，原两计划/两回执/两付费查询不重复。接受决策哈希与真实工具集在脚本恢复中逐条核对。

按单代理选择完成整体自审：检查权限边界、硬事实、角色与计量、schema克隆、协议恢复、持久化与停止预算；发现的停止后工具重绑定问题已修正并回归。

本机OpenSpec CLI未提供verify命令，未声称运行OpenSpec verify。使用严格validate并人工逐项核对以下矩阵，程序测试与规格核对分别执行。

| 正式要求/场景 | 实现与验证证据 |
|---|---|
| 无冲突往返、已有记忆 | FlowState/自动handoff；真实sparse、memory_defaults、normal_roundtrip；默认优先级与完整回复检查 |
| 政策唯一例外 | MainUsed/PolicyContinuationUsed状态及JSON恢复测试；真实policy全部实际辅助调用单列 |
| 总结直接重排、南京中转 | 独立summary profile与route_tasks；脚本3次路径；真实replan重复、固定返程/硬一晚/活动及各段时刻核对 |
| 错误不重进Main | TestMainRecoveryHandsToSummary、真实参数错误记录；实际总结提示/工具/hash核验 |
| 查询及回执不另调模型 | 自动query jobs、ReadyReply；纯query保持无计划，正常选择直接返回保存回执 |
| 范围与硬要求保护 | TestPlanningSummaryProfileAndScope；TestPlanningSummaryRouteAndHardRequirements；错误ID、硬预算/日期/晚数/活动及原输入零修改负例 |
| 恢复不重复模型/写入/查询 | TestTravelSourceRecoveryBatch/Jobs；角色JSON、接受决策哈希、attempt隔离 |
| 来源保真及运行扩展 | 冻结提示/schema SHA断言、新提示/profile纳入指纹；旧verifier范围不变，新角色独立授权 |

## 本地部署与交付

干净代码提交 `5cd274be85414f75082160584b7d5dadd2436a47` 构建，Go元数据 `vcs.modified=false`。部署前确认活动Run=0并核对旧后端精确路径，仅替换本项目后端；新PID23528，`/health/ready` ready，网页HTTP200。其他项目的IPv6端口未动。

测试地址：[小豆包](http://127.0.0.1:5173)。通过网页同源代理，使用独立测试账号、页面心跳、会话创建及完整执行接口输入“帮我规划明天去上海的旅途”。Run `a117a177-eb40-43e7-a84b-0c95ee493a36` succeeded，耗时8.08秒，重庆→上海→重庆、10-27至10-29住宿及活动完整保存；回复明确演示数据及酒店未核实房价/房态，没有追问。

只读数据库核对实际调用：Main1＋planning_summary1，总2次；两个接受决策，0历史读取、0模型拒绝，结束后活动Run=0。两个调用usage共21845 token。网页原始证据：`.cache/reports/travel-summary-web-2026-10-26.json` 与 `travel-summary-web-audit-2026-10-26.json`，摘要也纳入脱敏JSON。

沿用 `codex/travel-normal-flow`，基于main `2a4b3c6`。未合并main、未推送。原始失败和运行日志在忽略目录保留；正式验收文档与脱敏报告在Git内。

归档：已执行OpenSpec archive，变更归档至 `openspec/changes/archive/2026-10-26-fuse-travel-summary-replanning/` 并同步两份主规格。归档后全局复核还发现“有限编排/有限校正”旧段落含回Main语义，已在本次归档delta与主规格同步协调为用户已授权的新流程，防止正式规格自相矛盾。严格全量12/12、归档任务检查7/7通过。实施计划及任务均完成。
