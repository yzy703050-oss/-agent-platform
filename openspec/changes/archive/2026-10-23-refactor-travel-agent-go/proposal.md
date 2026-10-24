## Why

平台已经完成普通聊天、用户隔离和任务恢复，但差旅入口仍不可执行。现有 Python 差旅助手包含行程业务规则、证据校验及工具恢复能力，需要将这些能力用 Go 重构并纳入现有平台分层，使业务一致性和执行可靠性成为可验证的后端能力。

## What Changes

- 将差旅业务内核与 Agent 编排迁入当前 Go module，注册为独立的 `travel` Agent；继续保留 `general` 与 `travel` 两个入口，不实现主 Agent 委派。
- 沿用 Handler → Application/chat → AgentManager → Agent 实现的入口。差旅编排放入 `agent/agents/travel`，业务规则放入 `service/trip`，工具桥接放入 `agenttools/travel`，外部调用放入 `client`，数据访问由 `repository/postgres` 实现。
- 分阶段迁移结构化行程规划、局部修改、约束校验、候选证据、政策检索、事实输出，以及差旅偏好和上下文管理。分阶段验收不代表自动缩减最终范围。
- 直接调用用户现有 RAG 系统提供的 MCP 服务；不另建 RAG，不移植其索引、向量库或嵌入模型。实际连接方式及工具契约在用户提供服务后确认。
- 共享平台身份、Conversation、Run、事件、页面租约和 PostgreSQL；差旅执行适配 Eino 断点机制，并为业务写入增加可追溯回执与幂等保护。
- 使用增量迁移扩展现有数据库，不重建现有开发库，不复用 Python 的 SQLite 身份或会话系统。按用户“旧的信息数据都不要了”的要求从新数据开始，不导入 Python 的行程、偏好、历史和执行断点；不删除原项目数据。
- 按用户要求精简测试：在当前项目内验证 Go 迁移，不逐个移植 Python 的上千个测试；复用平台已有测试，对差旅新增的业务规则、隔离、事务及恢复风险补充少量有意义的用例，不以测试数量作为交付目标。

## Capabilities

### New Capabilities

- `travel-planning`: 结构化行程、硬软约束、增量修改、业务版本校验及事实输出。
- `travel-evidence`: 外部查询、来源与未知值表达、候选归属及新鲜度校验。
- `travel-policy`: 通过现有 RAG MCP 服务检索政策证据并约束差旅判断。
- `travel-execution`: 差旅阶段编排、平台执行接入、写入回执和恢复一致性。
- `travel-memory`: 差旅偏好与派生记忆的业务所有权、来源和上下文隔离。

### Modified Capabilities

- `identity-and-conversations`: 将差旅预留入口扩展为接入完成后可执行的独立 Agent 入口，继续保留未就绪时明确不可用的行为。

现有 `agent-execution`、`task-recovery`、`context-isolation`、`streaming-and-delivery` 的要求继续适用；本变更不放宽其暂停、权限、隔离或持久化保证。

## Impact

- 扩展现有 `backend/internal` 分层、新增差旅业务表与 Repository；调整聊天可用性判断、bootstrap 注册和前端差旅入口状态。
- 外部依赖包括模型服务、车次/酒店等供应方及现有 RAG MCP 服务。真实协议、凭据与能力需单独核验，不能以测试替身冒充真实接入验收。
- Python 项目 `G:\简历项目\差旅出行助手-v0` 作为行为及测试用例参考，本次不修改其文件或数据库。
- 文档当前为设计草案。服务接入信息详见 `design.md` 的 Open Questions；在规格与设计审阅、详细实施计划和执行方式确认前，不开始产品代码实现。

## Non-goals

本次不实现 General → Travel 委派、订票支付、退款、Python 执行断点转换、多实例调度或新的 RAG 系统；不引入 Redis、消息队列或单独的差旅后端服务作为前置条件。
