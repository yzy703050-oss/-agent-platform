# 契约设计材料验证记录

当前增量：2026-11-06在原Schema补7个运行v1定义和样例37～43；重新运行validate_contracts.py通过43正例/47反例、原多城一致性及新增运行身份/hash对应检查。可信_meta的Go→Python传输与Eino恢复另有隔离实验，见[当前技术复核](../engineering-review.md)。这些不证明真实授权、并发、JCS通用编码或数据库事务。下文36/37为旧范围历史结果。

> 以下保留2026-11-03验证历史。2026-11-05异步运行元数据和Maintenance阶段见[工程协议](../engineering-protocol.md)；现有JSON Schema/样例尚不覆盖新运行envelope，不把这些正反例冒充新链路验收。新材料检查见[工程复核记录](../engineering-review.md)。

日期：2026-11-03。范围为原专业规划v1契约与最新双入口共享查询记录投影，不是产品接口验收。此前33份正例/32个反例的记录由本轮结果替代。

## 1. 实际结果

- Schema元校验、36份正例、37个反例、跨示例引用及多城fixture一致性通过。
- OpenSpec strict validate退出码0。
- 三个阅读版JSON块按TravelToolArgs、PlanPatch、UserMemorySnapshot校验通过。
- 本地链接目标、围栏、空白及UTF-8检查通过；完整范围见数据库verification记录。

PowerShell复现：

```powershell
$env:PYTHONPATH = Join-Path $env:TEMP 'codex-agent-contract-validation'
python openspec/changes/refactor-agent-kernel-plan-execute/contracts/validate_contracts.py
openspec validate refactor-agent-kernel-plan-execute --strict
```

直接用默认Python运行曾因缺少jsonschema失败；复用此前临时目录依赖后通过。项目依赖未改变，未为本轮安装新的依赖。

## 2. 本轮新增覆盖

34是General入口直接查询的调用记录，35是其共享事实版本，36是Travel入口直接查询的受理记录。两种入口都不强制plan/task/delegation身份，当前样例明确标demo，不能作为实时天气。

SharedToolCallRecord与SharedToolEvidenceRecord是内部存储投影，工具模型参数继续使用注册Schema；它们不是新的MCP通用协议。正例检查调用/证据的会话及版本引用。新增反例覆盖直接查询被强加plan/delegation、成功无证据、未知live/demo模式和共享证据被强加Task。

原01～08改为用户明确建立住宿安排的规划场景，原ExecutionRequest/Result仍用于计划Executor；不把纯查询改成规划DTO。其余Item仅查询、来源、Patch、Task版本、选择、记忆权威和覆盖反例继续保留。

## 3. 限制

Schema和局部夹具检查不证明工具注册参数、用户权限、真实来源、摘要保真、RAG政策适用性、供应方、数据库事务、Eino恢复或上线质量。没有运行产品apply、实现verify或archive；具体运行适配与完整共享查询消费契约仍待技术复核。
