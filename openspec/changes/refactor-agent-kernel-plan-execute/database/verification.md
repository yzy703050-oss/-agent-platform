# 双入口与数据库分层：设计材料核对记录

> 历史记录：以下是2026-11-03材料验证，保留原范围/输出。2026-11-05已增加Job/Outbox/调用账并将周期检查移到Maintenance，当前结果见[工程复核记录](../engineering-review.md)，不以本页旧19表/入口检查描述覆盖新设计。

日期：2026-11-03。范围：本change的Specs、Design、数据库附录、迁移映射、组件记录投影和五份阅读/导航文档。本次按用户确认省略独立决策表，核对五层19表；替代此前20表材料检查结论，不是数据库实施验收。

## 1. 本轮实际核对

| 检查 | 方法与结果 |
| --- | --- |
| 正式规格结构 | openspec validate refactor-agent-kernel-plan-execute --strict，退出码0 |
| 组件材料 | validate_contracts.py通过Schema元校验、36份正例、37个反例、跨示例引用及多城fixture一致性 |
| 逐表职责 | 数据库字典L1～L5合计19个唯一表章节，覆盖4/7/2/4/2分层 |
| 现有表兼容映射 | 从backend/migrations/*.up.sql提取的18张表均出现在迁移矩阵 |
| 阅读材料 | 21份Markdown本地链接目标存在，围栏配对、UTF-8及行尾空白检查通过；3个内嵌JSON按真实契约类型校验 |
| 变更范围 | 对本轮修改前保存在系统临时目录的设计快照核对；文件位于docs/和本OpenSpec change，未修改产品源码或迁移SQL |
| Git空白 | git diff --check退出码0；多数材料未跟踪，另行检查其实际内容，不将Git空输出当作内容证据 |

材料检查脚本位于系统临时目录codex-agent-dual-entry-material-check.py，可通过PowerShell运行：

```powershell
$env:PYTHONPATH = Join-Path $env:TEMP 'codex-agent-contract-validation'
python openspec/changes/refactor-agent-kernel-plan-execute/contracts/validate_contracts.py
python "$env:TEMP\codex-agent-dual-entry-material-check.py"
openspec validate refactor-agent-kernel-plan-execute --strict
git diff --check
```

默认Python环境没有jsonschema；本轮复用此前系统临时目录中的验证依赖后通过，没有安装或改变项目依赖。临时材料检查脚本未作为产品程序提交；检查范围和输出另以本文件记录。

## 2. 新设计的逐项自审

| 最新边界 | 数据库落点 |
| --- | --- |
| General与差旅两个入口 | conversations.agent_type；根Thread.agent_kind匹配入口 |
| 两入口纯查询不建计划 | tool_calls/tool_evidence无plan/task前置；没有计划线程或规划delegation |
| 两入口规划用同一子流程 | agent_delegations记录调用，差旅入口不先调用General；一个外层Run |
| 入口与计划上下文不同 | thread_role=entry/plan_work；travel_plans唯一绑定计划线程 |
| 先查询后规划 | 真正证据引用及条件/时效/来源检查；执行项记录计划接受，不改证据原归属 |
| 工具成功不等于计划完成 | tool_calls状态与travel_execution_steps适用性分开；旧Task/generation结果不可覆盖当前任务 |
| 政策准备查询 | 共享工具证据保存实际依据，preparation_context保存核验状态和引用，不伪造酒店执行项 |
| 计划修改可靠提交 | 独立决策表新流程停写；受理参数和必要依据在actions，实际Patch在revisions，必要错误在events；回执和计划同事务 |
| Task定义变化 | 所属全部Item重新查询，其他Task不因整体升版重查；不让共享缓存绕过规则 |
| 用户统一记忆 | 两表、用户快照、来源/撤回/CAS；根Thread两个覆盖游标 |
| 双入口记忆检查 | 每入口主线程独立10轮，由当前入口Agent判断，不额外增加General必经调用 |
| 新库部署 | 2026-11-07用户取消旧数据保留；新库不导入旧业务表或断点，保护新版本事实 |

用户已确认省略独立决策表，目标由20调整为19；具体字段、锁和约束仍须技术复核，不能视为已逐字段验收。原任务的历史完成记录保留，新完成项只表示设计交付，技术复核/实施父项未勾选。

补充核对：新格式actions没有到旧travel_decisions的外键，Replanner历史读取不依赖旧表；memory_operations.decision_key继续作为记忆检查幂等身份，未误删。旧18表迁移映射继续包含travel_decisions，但标记为仅legacy历史读取、新流程停写；不执行物理删除。组件Schema没有决策表依赖，本次不修改JSON类型或样例。

## 3. 明确没有验证的内容

没有运行产品模型、供应方或远端MCP，也没有读取真实用户数据、执行DDL、backfill、升级/回退、PostgreSQL外键/锁/并发、Eino嵌套checkpoint、权限语义、摘要保真或后台任务竞争测试。Markdown锚点可达性及Mermaid渲染未测试。

未进入apply、产品实现verify或archive，未生成新的详细实施计划。CLI结构validate和Schema正反例是材料验证，不能代替产品测试。后续实施仍按用户给定OpenSpec/Superpowers流程进行。
