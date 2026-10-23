## Why

构建一个用于展示 Go 后端工程能力的 Agent 应用平台：复用 Eino 的智能执行能力，重点实现用户隔离、业务边界、流式通信、任务生命周期、持久化断点恢复和可验证的故障处理。当前只有目录骨架，没有产品代码和数据库。

## What Changes

- 建立 Go + Gin + PostgreSQL 后端和 React + Vite 前端，提供注册、登录、登出、个人设置、会话与历史。
- Service 与 Agent 并列；复杂聊天用例经 Application 协调；Repository 统一实现数据访问，业务写入仍经过 Service。
- 普通聊天参考 `cloudwego/eino-examples/quickstart/chatwitheino`，沿用 Eino Agent、TurnLoop、Checkpoint/Resume；第一版启用聊天和只读业务工具，不启用本地 shell、文件操作、文档 RAG、审批和 MCP。
- 实现 Run 状态机、幂等提交、单会话执行控制、SSE、页面级租约、关闭页面后的安全暂停，以及重新打开后的手动恢复。
- 暂停会话中发送新问题，结束旧任务并使旧断点失效；旧历史保留展示，但旧任务消息、输出与工具结果永久退出后续模型上下文。
- 提供差旅独立入口和不可用状态，不实现差旅适配器或模拟行程。未来 Python 差旅内核通过平台内部接口接入统一持久化。
- 交付数据库迁移、本地启动配置、关键自动化测试与演示步骤。用户机器尚未安装 PostgreSQL 和 Docker，安装与真实数据库验证单独记录，不用内存测试冒充数据库验证。

## Capabilities

### New Capabilities

- `identity-and-conversations`: 身份会话、个人设置、入口固定的会话、历史与用户隔离。
- `agent-execution`: Eino 聊天、只读工具、平台执行生命周期、幂等和执行互斥。
- `task-recovery`: 页面级执行归属、安全暂停、持久化断点、手动继续、进程重启后的可恢复性判定。
- `context-isolation`: 展示历史与执行上下文分离，被新问题替代的任务不再参与上下文。
- `streaming-and-delivery`: SSE 事件、状态查询、日志、基础指标、部署和可复现验收。

### Modified Capabilities

无；这是首个行为变更。

## Impact

- 新增 `backend/` Go module、API、Repository、数据库迁移和 Eino 适配；新增 `frontend/` React 应用。
- 新增运行依赖 PostgreSQL；Redis、队列、微服务和自动调度不作为 MVP 前提。
- 模型连接首版由服务端环境变量配置；个人凭据中心、MCP、长期记忆自动抽取和差旅重构属于后续独立变更。
- 实施必须先完成规格审阅和执行方式选择；当前任务清单不表示代码已完成。

## Non-goals

低代码 Agent 编排、General 自动调用 Travel、远程差旅业务实现、任意本地代码执行、任意时刻无损冻结、自动重放未知结果的副作用操作、完整 Token 重放、断点跨任意版本迁移。
