# Go 差旅重构验收记录

日期：2026-10-23。对应 change：`refactor-travel-agent-go`；单代理实施及整体自审。当前仍在交付核对中，未归档；本记录不把历史 MVP CI 当成本次提交的通过证据。

## 实现与验证映射

| 源行为 | 当前实现与证据 |
| --- | --- |
| B01–B04 多段、返程、日期、活动、约束与金额 | `TestTripRules` 表驱动场景；活动修改不影响原候选，日期修改只失效关联选择，未知金额不作0；真实数据库 `TestTravelTransactions` |
| B05–B06 查询复用、隔离及过期候选 | `TestTravelQueries`：归属、缓存、规范化、绑定、迟到结果、瞬态最多重试一次；`Inspect` 在恢复和最终展示重新核验保存候选时效 |
| B07 政策证据 | `TestPolicyEvidence`、`TestPolicyProtocol`、工具超时用例；真实 `TestPolicyMCPReal` 从独立演示集合返回 `ok / 1 fragment`，具有来源与片段 ID |
| B08–B10 编排、事实及断点 | `TestTravelWorkflow`、`TestTravelWorkflowRecovery`：决策先保存、选择真实证据、跨实例/attempt恢复、旧有效断点重放不增加修订、累计迭代不重置、未知结果拒绝自动重放 |
| B11 页面生命周期 | 平台既有租约、恢复、SSE 检查复用；差旅页面内切换继续已在实际浏览器验证，关页与手动继续的最终浏览器结果待补 |
| B12–B14 任务隔离与记忆 | `TestTravelMemoryIsolation`、`TestTravelContextBudget`、`TestTravelMemoryMaintenance`：来源过滤、混合摘要失效、有效版本回退、明确撤回保护、保守推断和完整工具配对 |
| B15 原平台回归 | 完整 Go/真实 PostgreSQL、vet/build 和前端检查通过；Linux race 仍需当前提交 CI 结果 |
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
- 实际浏览器：已验证独立差旅入口、真实行程保存与选择、页面内切换继续；已验证政策无命中。最新演示模式及关页恢复待最终验收。
- Linux race CI：等待当前提交推送后的结果。
- OpenSpec：CLI 1.12 未提供 verify 工作流，采用人工逐项核对需求、设计、任务与证据；不声称运行了 OpenSpec verify。

## 尚未实现及部署限制

主 Agent 委派、真实订票/酒店预订/支付/报销审批、正式企业政策接入和自动完整合规判断、网页搜索供应方、Redis/队列/多实例、动态 Agent/MCP 管理均未实现。天气客户端已有协议验证，真实天气外网冒烟未取得证据，演示模式天气可用。Docker/Compose 尚未实际运行，本机走原生 PostgreSQL。

框架版本、工具或模型变化可使旧断点不兼容，历史仍保留；强制取消、超时或未知业务结果不会承诺继续。正常关闭页面请求安全点暂停，不能保证任意模型 Token 处无损停止。当前变更在剩余验收完成前保持未归档。
