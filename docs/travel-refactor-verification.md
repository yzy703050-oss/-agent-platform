# Go 差旅重构验收记录

日期：2026-10-23。对应 change：`refactor-travel-agent-go`；单代理实施及整体自审。本次范围已完成验收并归档；本记录不把历史 MVP CI 当成本次提交的通过证据。

## 实现与验证映射

| 源行为 | 当前实现与证据 |
| --- | --- |
| B01–B04 多段、返程、日期、活动、约束与金额 | `TestTripRules` 表驱动场景；活动修改不影响原候选，日期修改只失效关联选择，未知金额不作0；真实数据库 `TestTravelTransactions` |
| B05–B06 查询复用、隔离及过期候选 | `TestTravelQueries`：归属、缓存、规范化、绑定、迟到结果、瞬态最多重试一次；`Inspect` 在恢复和最终展示重新核验保存候选时效 |
| B07 政策证据 | `TestPolicyEvidence`、`TestPolicyProtocol`、工具超时用例；真实 `TestPolicyMCPReal` 从独立演示集合返回 `ok / 1 fragment`，具有来源与片段 ID |
| B08–B10 编排、事实及断点 | `TestTravelWorkflow`、`TestTravelWorkflowRecovery`：决策先保存、选择真实证据、跨实例/attempt恢复、旧有效断点重放不增加修订、累计迭代不重置、未知结果拒绝自动重放 |
| B11 页面生命周期 | 平台既有租约、恢复、SSE 检查复用；差旅页面内切换继续已在实际浏览器验证，页面离开/重开显示 paused，手动继续同一 Run 的 attempt2并完成，开发库状态一致 |
| B12–B14 任务隔离与记忆 | `TestTravelMemoryIsolation`、`TestTravelContextBudget`、`TestTravelMemoryMaintenance`：来源过滤、混合摘要失效、有效版本回退、明确撤回保护、保守推断和完整工具配对 |
| B15 原平台回归 | 完整 Go/真实 PostgreSQL、vet/build、前端和最终代码043c61d的Linux race CI全部通过 |
| 用户新增演示模式 | `TestDemoFactsRemainLabelled`、`TestTravelDemoQueries`、TravelResult 组件检查：演示标识贯穿保存选择和输出，live/demo 缓存隔离，酒店价格/库存未知 |

这些场景组合复用现有 fixture，没有搬入旧 Python 的上千个测试。新增测试针对业务和一致性边界，不为每个源函数另建测试。

## 实际依赖

- 开发 PostgreSQL 已顺序追加迁移004；既有001–003未改动。测试使用独立数据库中的随机 schema，真实事务、并发修订、回执原子性及升级/回滚均有覆盖。
- 聚合数据真实车次冒烟返回4条候选，来源为聚合数据；高德返回25个酒店地点，无价格或可售库存保证。
- 真实模型差旅冒烟产生1个行程、2次修订；浏览器还验证了实际选择候选及最终输出，不将模型声称付款的文本作为事实。
- 原 RAG 默认配置的云嵌入连接失败。检查发现已有 `config/travel_policy.yaml` 和本地 BGE，改用该配置后，自编 `docs/fixtures/demo-travel-policy.txt` 成功导入 `agent_platform_demo`，1个片段。平台真实 MCP 调用返回该来源及片段。
- 演示文档、主体和金额均为虚构，政策命中不等于真实企业合规。平台未修改原 RAG 源代码、未迁移 Python 历史数据。
- 本机显式设置 `TRAVEL_QUERY_MODE=demo`。设回 live 并重启可使用真实供应方；模式不会因网络失败自动变化。
- 本地 BGE/MCP 冷启动验收约18秒，本机 `TOOL_SECONDS=30`；默认仍10秒。缺失、超时和未命中都保留未核验原因。

原始命令输出位于忽略目录 `.cache/reports`：`travel-train-real.log`、`travel-hotel-real.log`、`travel-model-real.log`、`travel-demo-rag-real.log`、`demo-db.log`、`demo-red.log`、`demo-green.log` 等。报告与提交均不包含密钥、Cookie、页面令牌或 DSN。

## 整体自审发现与修正

1. 已保存行程读取增加有效 Session 检查，撤销 Session 无法直接读取。
2. 保存候选在恢复和输出时重新检查来源有效性及过期；不能用旧保存价格冒充当前事实。
3. 非流式强制停止也不能标为可恢复；共享 Runner 对升级取消统一保守处理。
4. 独立查询和政策答复从实际工具结果生成，丢弃模型编造的付款与报价结论；恢复保留最后工具引用。
5. 政策工具超时返回未核验观察；查询只对瞬态失败有界重试，非法格式不重试或生成候选。
6. 摘要与低频偏好分析仅使用允许来源，明确值和撤回记录优先。
7. 演示标识在证据与候选上持久化，选中后仍清晰显示；默认真实模式不会静默模拟。

上述修正的相关用例和受影响回归已运行。最终统一回归及浏览器、CI 结果如下。

## 最终验证状态

- `scripts/verify.ps1 -SkipNpmInstall`：完整 Go / 真实 PostgreSQL、vet/build、前端测试和构建 PASS。
- 实际浏览器：已验证独立差旅入口、真实行程保存与选择、页面内切换继续；已验证政策无命中。最终演示同时显示车次、天气、政策来源及演示标识；页面离开/手动继续和新问题替代均已与开发库核对。
- Linux race CI：最终代码提交 043c61d 的 [Actions](https://github.com/yzy703050-oss/-agent-platform/actions/runs/39963716906) 已通过，含真实 PostgreSQL race/vet/build 和前端。
- OpenSpec：CLI 1.12 未提供 verify 工作流，采用人工逐项核对需求、设计、任务与证据；不声称运行了 OpenSpec verify。

## 尚未实现及部署限制

主 Agent 委派、真实订票/酒店预订/支付/报销审批、正式企业政策接入和自动完整合规判断、网页搜索供应方、Redis/队列/多实例、动态 Agent/MCP 管理均未实现。天气客户端已有协议验证，真实天气外网冒烟未取得证据，演示模式天气可用。Docker/Compose 尚未实际运行，本机走原生 PostgreSQL。

框架版本、工具或模型变化可使旧断点不兼容，历史仍保留；强制取消、超时或未知业务结果不会承诺继续。正常关闭页面请求安全点暂停，不能保证任意模型 Token 处无损停止。本次变更已同步主规格并归档，外部能力和部署限制不变。

浏览器补充：恢复任务B在重开时为 paused/attempt1/有效断点，手动继续后 succeeded/attempt2/断点失效，保存行程修订1→2，演示标识保留。一次冷启动政策长工具关闭和一次查询边界竞争未形成安全断点，均显示 interrupted 而非伪造继续，符合已确认的保守恢复边界。最终回复汇总多个独立查询的修正已先失败后通过相关工作流检查。任务完成后隐藏陈旧阶段提示的前端修正也已验证15/15通过并构建成功。

新问题替代浏览器证据：四段规划任务处于 paused 时直接发送新问题，旧 Run 变为 cancelled/context_eligible=false/断点valid=false；历史仍可见，新 Run 独立查询北京至天津。实际数据库与界面一致。

复现知识导入：在已有 RAG 项目目录，用该项目 Python 执行其 src.ingestion.pipeline.IngestionPipeline(load_settings('config/travel_policy.yaml'), collection='agent_platform_demo').run('本平台绝对路径/docs/fixtures/demo-travel-policy.txt')。使用原有本地 BGE 配置；CLI 的 dry_run 缺失不在本次修改范围，实际调用的是相同摄入内核而非另建索引实现。

最终证据：代码提交043c61d本地完整verify与Linux CI均通过，数据库集成未跳过，前端15/15。合成浏览器请求同时返回车次100/150元、杭州演示天气22°C及演示政策片段来源，查询和政策互不覆盖。截图位于本地忽略的 .cache/reports/travel-demo-final.png，页面保留可查看。已完成六份delta需求、设计D1–D12及B01–B15人工核对；采用作者整体自审，无独立子代理评审。默认live、显式demo和未配置网页不可用的边界均已说明。

归档：openspec archive refactor-travel-agent-go --yes 成功，创建 openspec/changes/archive/2026-10-23-refactor-travel-agent-go 并同步六份主规格。归档时唯一未勾选项为归档自身，完成后更新为32/32；没有忽略失败测试或未完成产品任务。保持现有 feat/agent-platform-mvp 分支及当前工作目录，按已授权范围推送，不合并main。

## 实施取舍与影响

- 本机没有可用 Bash，以 PowerShell 等价记录任务 brief、BASE 和实际验证结果；任务状态已人工核对，未为流程安装额外 shell。
- Eino 恢复首先执行已接受的未完成工具，再请求下一次模型，不重新生成旧决策。恢复验证按真实边界设置，避免重复副作用。
- 通用与差旅共享 Runner 和原停止断言，保留少量 General fixture；框架升级需同步回归两个 Agent。
- 在途模型决策元数据可于 pausing 落库，业务写入仍严格验证 running/attempt；强制升级或不能确认安全点时明确中断，不承诺每个时刻都能继续。
- 使用 RAG 已存在的本地 BGE 配置，通过可信绝对路径传入子进程；部署须提供该配置及本地模型，未创建新 RAG 或复用旧用户数据。
- 原 RAG CLI 引用缺失的 dry_run 参数，改用它自身 IngestionPipeline 接口摄入，未修改外部源码；复现需使用本文列明的接口。网页供应方未提供时保持 unavailable，不新增空实现或假装真实接通。
