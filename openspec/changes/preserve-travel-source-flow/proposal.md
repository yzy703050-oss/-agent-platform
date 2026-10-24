## Why

前次 Go 迁移压缩了 Python 原版的提示词、工具契约和 Main→工具→Verifier 执行链路。2026-10-24 的真实模型测量中，独立查询 3/3 成功，但单程、多城和局部活动修改各 0/3 成功，不能继续以功能存在或无运行错误代表迁移等价。

用户要求继续迁移原差旅助手，保留原架构、设计、提示词和校正，在已确认的平台分层内实现。本变更修复迁移遗漏，不重新设计差旅产品。

## What Changes

- 以只读 Python 当前源码为基线，保留完整提示词及模型可见工具说明、参数和阶段授权；用基线摘要记录来源。
- 恢复 Main、Verifier、独立查询总结、重规划交接、结构化审查上下文、有限修复和实际回执绑定回复。
- 将原版业务校验、增量修改、条件来源/强度和候选规范化落在现有 Service/Repository/Client 分层。
- 保留平台身份、Run、页面暂停、手动恢复、任务替代隔离及统一 Repository；不导入 Python 用户数据。
- **BREAKING**：差旅内部工具协议和工作流指纹将更新，旧工作流断点不能强行恢复；可查看已有历史。平台 HTTP 入口和 General 行为不随之改变。
- 复用少量综合用例和固定模拟工具，运行真实模型测量，报告 Token、耗时、调用次数和实际业务结果。

## Capabilities

### New Capabilities

- `travel-source-fidelity`：原版提示词和工具契约保真、决策上下文、校正闭环及可追溯验收。

### Modified Capabilities

- `travel-execution`：细化有限编排，恢复批次预检、Main/Verifier 权限与重规划和停止语义。
- `travel-planning`：细化条件/增量修改及实际保存结果绑定，防止只回复成功而没有修改。

## Impact

涉及 `agent/agents/travel`、`agenttools/travel`、`agent/memory`、`service/trip`、`service/travelpreference`、`model`、必要的 Repository/Client 适配和 bootstrap 配置；不新建 Go module、Python 差旅服务或第二个任务系统。不改动原 Python 项目，不运行 Python 对照模型评测，不迁入上千个测试。
