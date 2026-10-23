# 小豆包 · Go Agent Platform

一个以 Go 后端工程能力为重点的 Agent 平台 MVP。前端使用 React，普通聊天使用 Eino，用户、会话、任务、消息、事件与执行断点统一存储到 PostgreSQL。

当前提供通用聊天与差旅预留入口。通用聊天已接入真实模型，支持工具调用、SSE、任务暂停、安全断点恢复和历史会话；差旅尚未接入。

项目重点是让模型调用进入具备权限、模块边界、事务、并发控制、状态管理与故障恢复能力的后端系统。

**状态基线：2026-10-23，单实例 MVP 已完成并归档。** 本机 84 项后端、13 项前端测试通过；真实 PostgreSQL、真实模型和浏览器流程已验收；Linux CI race、vet、前后端构建通过。Docker 配置已提供，未实际运行验证。

[代码分支](https://github.com/yzy703050-oss/-agent-platform/tree/feat/agent-platform-mvp) · [通过的 CI](https://github.com/yzy703050-oss/-agent-platform/actions/runs/39922467829) · [验证记录](docs/verification.md) · [真实模型验收](docs/real-model-acceptance.md)

## 阅读导航

- [当前做了什么](#当前做了什么)
- [技术栈](#技术栈)
- [分层架构与职责](#分层架构与职责)
- [关键调用流程](#关键调用流程)
- [暂停与恢复](#暂停与恢复)
- [历史与-memory](#历史与-memory)
- [数据一致性](#数据一致性)
- [安全预算与诊断](#安全预算与诊断)
- [实际目录与规划差异](#实际目录与规划差异)
- [本地运行](#本地运行)
- [api-与事件协议](#api-与事件协议)
- [验证与演示](#验证与演示)
- [尚未实现和未验证](#尚未实现和未验证)
- [后续扩展与正式文档](#后续扩展与正式文档)

## 当前做了什么

| 能力       | 当前实际行为                                                        |
| ---------- | ------------------------------------------------------------------- |
| 身份与设置 | 注册、登录、登出、当前用户、主题和回复偏好；数据按用户隔离          |
| 会话       | 创建、重命名、受控删除、游标分页和重开历史                          |
| 独立入口   | 点击入口创建新会话，历史会话保持原 Agent 类型                       |
| 普通聊天   | 服务端配置真实 OpenAI 兼容模型，无静默模拟回答                      |
| 业务工具   | 一个只读工具 `get_my_settings`，通过 SettingsService 读取本人设置   |
| 实时展示   | SSE 传输模型真实增量、工具状态与持久化任务事件                      |
| 页面内切换 | 换历史或 Agent 入口不暂停原任务                                     |
| 页面离开   | 离开通知与页面租约过期触发安全暂停请求                              |
| 暂停与继续 | 仅当执行已停止、有效断点已保存时才报告 paused；用户显式继续         |
| 重开       | 展示历史、已有输出和任务状态，不自动启动模型                        |
| 新问替代   | 结束暂停任务并使旧断点失效，历史可见但旧任务不再进入后续上下文      |
| 启动对账   | 有兼容断点等待手动恢复，无有效断点标记 interrupted                  |
| 后端一致性 | 幂等、会话互斥、事务、状态条件更新和执行尝试围栏                    |
| 诊断与交付 | 结构化日志、基础指标、健康检查、启动脚本、测试和 CI                 |
| 差旅       | 只有独立入口和不可用状态，没有 TravelAdapter、Python 服务或模拟行程 |

![真实模型任务重开后保持暂停](docs/screenshots/real-model-paused.png)

## 技术栈

| 领域     | 当前使用                    | 作用                                                  |
| -------- | --------------------------- | ----------------------------------------------------- |
| 后端     | Go，最低 1.26.0             | HTTP、并发执行与生命周期                              |
| HTTP     | Gin 1.12.0                  | 路由、中间件、JSON 和 SSE                             |
| Agent    | Eino 0.9.0-beta.1           | ChatModelAgent、TurnLoop、工具循环、Checkpoint/Resume |
| 模型     | eino-ext OpenAI 组件 0.1.12 | OpenAI Chat Completions 兼容服务                      |
| 存储     | PostgreSQL 17、pgx/v5       | 连接池、SQL、显式事务与约束                           |
| 前端     | React 19、TypeScript、Vite  | 登录、历史、入口、聊天与任务控制                      |
| 前端测试 | Vitest、Testing Library     | 组件与交互验证                                        |
| 脚本     | PowerShell 7                | 配置加载、启动、验证                                  |
| CI       | GitHub Actions              | Linux race、PostgreSQL、vet 与前端验证                |
| 可选部署 | Dockerfile、Compose、Nginx  | 已提供配置，未实际运行验收                            |

精确版本见 [go.mod](backend/go.mod)、[go.sum](backend/go.sum)、[package.json](frontend/package.json) 和 [锁文件](frontend/package-lock.json)。

当前是模块化单体、单实例 API，没有 ORM、Redis、消息队列或向量数据库。正式状态在 PostgreSQL，活动执行句柄、事件通知和部分配额在 Go 进程内。

## 分层架构与职责

### 1. 整体结构

```mermaid
flowchart TD
    UI[React] --> HTTP[Router / Middleware]
    HTTP --> H[Handler]
    H --> S[传统 Service<br/>Auth / User / Conversation / Settings]
    H --> APP[Application / Chat]
    H --> M[AgentManager<br/>注册选择 / Run / 配额]
    APP --> S
    APP --> M
    M --> MEM[Memory / ContextBuilder]
    M --> A[GeneralAgent 平台适配]
    A --> E[Eino ChatModelAgent / TurnLoop]
    E --> C[Model Client]
    C --> LLM[外部模型 API]
    E --> T[SettingsTool]
    T --> S
    E --> CP[Checkpoint Store 适配]
    M --> STORE[消费者定义的 Store 接口]
    MEM --> STORE
    CP --> STORE
    S --> STORE
    STORE --> R[PostgreSQL Repository]
    R --> DB[(PostgreSQL)]
    M -. 未来接入 .-> TR[TravelAdapter 预留]
```

箭头是调用关系，不要求所有请求都经过全部节点。`bootstrap` 负责组件构造与依赖注入，Store 接口按消费方定义。

### 2. Service 与 Agent 为什么并列

| 层或子系统        | 负责                                     | 不负责                  |
| ----------------- | ---------------------------------------- | ----------------------- |
| Router/Middleware | 路由、认证、CSRF、来源、日志、基础限流   | 业务规则和推理          |
| Handler           | HTTP 参数、Scope、页面令牌、响应、SSE    | SQL、模型循环           |
| Application       | 跨业务与 Agent 的用例协调                | 直接操作具体 Repository |
| Service           | 身份、会话、设置等确定性业务             | Agent 内部循环          |
| AgentManager      | 任务准入、选择、状态、运行句柄、暂停恢复 | 重写 Eino 推理内核      |
| Agent 实现        | 平台契约与 Eino 的消息、事件、执行适配   | 用户和设置业务规则      |
| Repository        | SQL、事务、查询和数据库约束              | 反向调用上层业务        |
| Client            | 外部协议客户端                           | 决定平台任务终态        |

实际路径：

```text
修改设置：Handler → SettingsService → Store → Repository
提交聊天：Handler → Chat Application → ConversationService / AgentManager
暂停继续：Handler → AgentManager → Execution → Eino
```

Handler 按接口职责分派，复杂聊天经过 Application 协调。并列子系统可以复用同一业务 Service，不意味着复制业务规则。

### 3. Agent 怎样分层

| 位置                 | 当前职责                                 | 代码入口                                                                                                                 |
| -------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `contracts`          | Agent、Execution、事件与预算契约         | [agent.go](backend/internal/agent/contracts/agent.go)                                                                    |
| `manager`            | 注册、选择、提交、状态、租约、通知、指标 | [manager.go](backend/internal/agent/manager/manager.go)、[lifecycle.go](backend/internal/agent/manager/lifecycle.go)     |
| `agents/general`     | 创建独立执行器，转换消息，封装框架       | [agent.go](backend/internal/agent/agents/general/agent.go)、[runner.go](backend/internal/agent/agents/general/runner.go) |
| Eino 内核            | 模型/工具循环和内部恢复                  | 固定版本框架提供                                                                                                         |
| `memory`             | 授权历史查询、完整任务组裁剪             | [context.go](backend/internal/agent/memory/context.go)                                                                   |
| `agenttools`         | Service 到 Eino Tool 的桥接              | [settings_tool.go](backend/internal/agenttools/settings_tool.go)                                                         |
| `ports` 与消费方接口 | 运行数据和断点存储契约                   | [stores.go](backend/internal/agent/ports/stores.go)                                                                      |
| Repository           | SQL、事务、执行围栏与对账                | [数据访问目录](backend/internal/repository/postgres/)                                                                    |
| Client               | 服务端模型配置和协议客户端               | [client.go](backend/internal/client/llm/client.go)                                                                       |

逻辑上是 Manager → Agent 实现 → 内核 → 能力 → 数据接口/访问实现 → 基础设施。实际存在合理旁路：Manager 保存 Run，ContextBuilder 在执行前构造上下文，不必穿过内核。

注册表目前静态注册，按会话 `agent_type` 选择。没有根据 Query 自动调用差旅的语义路由或多 Agent 委派。

### 4. 对 Eino example 复用了什么

参考 [chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)，使用 ChatModelAgent、TurnLoop、标准 schema.Message 和 Checkpoint/Resume；平台负责事件消费、生命周期与存储适配。

没有原样搬入整个示例：没有完整 DeepAgent、Shell、文件、文档 RAG 或审批，没有把示例内存断点和 JSONL 当成正式平台存储，也没有另写一套替代 Eino 的推理循环。

### 5. 如何共享业务和底层数据

```text
设置 API → SettingsService → Store → PostgreSQL Repository
Eino → SettingsTool → SettingsReader → SettingsService → 同一 Repository
```

设置工具只接受 `{}`，用户身份取自后端可信 Scope，模型不能传 user_id 越权；读取还校验当前 Session。

Repository 是统一访问实现，但业务操作经 Service，Run/消息/事件/断点经 Store，外部服务经 Client。具体工具当前直接注入 Eino ToolsConfig；`agent/tools/` 仅预留，没有通用动态工具管理器。

## 关键调用流程

### 正常聊天

```mermaid
sequenceDiagram
    participant U as React
    participant H as Handler / Application
    participant M as AgentManager
    participant R as Repository / PostgreSQL
    participant E as Eino Execution
    U->>H: content + page_id + Idempotency-Key
    H->>H: 校验 Session / CSRF / 页面令牌 / 会话访问
    H->>M: 提交任务
    M->>R: 幂等查找、事务准入、保存用户消息
    M->>R: 查询本次允许使用的历史
    M->>E: 创建独立 Execution 并启动
    M-->>U: 返回 Run，提交 HTTP 请求结束
    U->>H: 订阅 SSE
    E-->>M: 模型增量、工具消息、结果
    M->>R: 节流保存输出与持久化工具事件
    M-->>U: SSE 文字与状态
    M->>R: 事务提交最终回答、成功状态、完成事件
    M-->>U: 通知读取已提交结果
```

会话决定 Agent 类型；提交消息不能改变该会话的 Agent。相同内容和幂等键的重试返回已有 Run，同键异参返回冲突。

执行使用 Manager 独立上下文，不绑定提交 HTTP 或 SSE 连接的寿命。关闭 SSE 只结束订阅，页面是否离线另由租约判断。最终回答、成功状态和事件共同提交后才发布完成通知。

### 流式事件

| 类型          | 保存方式                   | 重连行为                            |
| ------------- | -------------------------- | ----------------------------------- |
| Run/工具状态  | 先保存 run_events，再通知  | 按递增 seq 查询、补齐和去重         |
| message.delta | 临时增量，聚合文本另行保存 | 不承诺逐 Token 重放，重开查已有内容 |

SSE 采用先订阅再查持久事件，按 seq 去重，处理查询与订阅交接竞争；之后通过通知和定时查询补齐。慢消费者使用有界缓冲，溢出关闭订阅，状态仍能 GET。

EventHub 仅在当前 Go 进程内，不是跨节点事件总线，也不是所有 Token 的永久日志。

## 暂停与恢复

### 平台恢复与 Agent 恢复

| 层次 | 管理内容                                             | 作用                                   |
| ---- | ---------------------------------------------------- | -------------------------------------- |
| 平台 | Run、页面、attempt、历史、已有输出、事件、断点元数据 | 恢复显示、权限、并发继续互斥和恢复资格 |
| Eino | 框架内部执行 payload                                 | 从兼容有效的边界恢复内核与待处理步骤   |

有聊天历史不等于有执行断点。平台保存 opaque bytes，关联框架版本、配置指纹、Run、用户、attempt，不自行重造框架快照。

### 页面关闭如何处理

- 新页面文档注册独立 page_id/page_token，绑定当前 Session。
- 默认心跳 5 秒、租约 20 秒，前端使用服务端 heartbeat_ms。
- 页面内切换不销毁租约；其他标签页不能替发起页续租。
- pagehide 尝试发离开通知；未送达时由租约过期兜底。
- 显式恢复后，归属转移到新页面。

关闭存在检测延迟，短任务可能先自然完成，不承诺关闭瞬间停止。离开通知也可能比租约超时更早触发暂停。

### 什么情况下才是 paused

1. Manager 迁移 running → pausing。
2. 请求 Eino 停止，等待框架确认取消已施加。
3. 等待执行实际退出与已有输出落库。
4. 确认本次 attempt 的有效断点已保存且可读。
5. Repository 提交 paused，界面才提供继续入口。

默认安全停止等待预算 15 秒。没有有效断点、保存失败或无法确认停止时明确 interrupted，不虚报暂停成功。

**不保证任意生成位置无损续写。** 固定 Eino 版本的强制截流探针曾出现已完成工具重放风险，所以平台拒绝这类不安全断点。已验证的是支持的工具边界恢复，不是任意 Token 续传。

### 重开、继续与进程重启

重开只查询历史、输出和状态。点击继续才校验权限、断点、配置指纹，竞争恢复权，增加 attempt、创建新 Execution；Run ID 不变，不重复创建用户问题。

启动先取得单实例数据库锁，再对旧任务对账：有兼容有效断点则等待用户继续，没有则 interrupted，不自动调用模型。突然崩溃不保证产生新断点。

```mermaid
stateDiagram-v2
    [*] --> accepted
    accepted --> running
    running --> succeeded
    running --> failed
    running --> timed_out
    running --> pausing
    pausing --> paused: 已停止且有效断点已保存
    pausing --> interrupted: 无法安全恢复
    paused --> resuming: 用户继续
    resuming --> running: 新 attempt
    resuming --> interrupted: 不兼容
    running --> cancelling: 用户结束
    paused --> cancelling: 用户结束
    cancelling --> cancelled
    cancelling --> interrupted: 无法确认停止
    paused --> cancelled: 被新问题替代
```

图是主要路径，异常重启还有对账迁移。paused 为可恢复非终态；succeeded、failed、timed_out、cancelled、interrupted 为终态。

## 历史与 Memory

展示历史和模型上下文是不同查询。ContextBuilder 查询当前用户当前会话中可用的任务，按完整 Run 从近到远裁剪，预算约 32,000 字节，保留工具调用/结果配对；不是精确 Token 计量。最新任务本身超预算会拒绝构造，不会截断工具配对。

例如：A 是更早完成任务，B 暂停后用户发新问题创建 C。平台事务取消 B，原因 `replaced_by_new_query`，断点失效，`context_eligible=false`，再创建 C。B 的问题、输出、工具和结果仍可查看，但不进 C 及后续任务自动组装的上下文；A 仍按正常预算选取，不是清空整个会话。

点击继续则恢复 B 原断点，是另一条路径。

Memory 当前只有授权历史查询与上下文构造，没有长期记忆提取、自动摘要、画像、跨会话记忆或向量检索。回复偏好作为设置保存并可由工具读取，没有独立自动个性化 Prompt 注入流水线。

## 数据一致性

### 数据表与迁移

| 表                | 用途                                                       |
| ----------------- | ---------------------------------------------------------- |
| users             | 用户与密码摘要                                             |
| sessions          | Session/CSRF 摘要、有效期和撤销                            |
| user_settings     | 主题和回复偏好                                             |
| page_leases       | 页面归属、令牌摘要、租约和关闭状态                         |
| conversations     | 所属用户、Agent、标题、删除状态                            |
| agent_runs        | 状态、幂等键、内容摘要、attempt、version、上下文资格和错误 |
| messages          | 用户/助手/工具消息、配对、分页序号、来源去重               |
| run_events        | 按 Run 递增的状态与工具事件                                |
| checkpoints       | 断点、框架版本、配置指纹、attempt、有效性                  |
| schema_migrations | 迁移版本                                                   |

迁移通过 Go embed 分发，启动时按版本在事务/advisory lock 下执行：001_platform 为基础表，002_message_delivery 为消息 attempt/去重，003_run_output 为已有输出。

PostgreSQL 是唯一正式存储。缺配置或不可达会拒绝启动，不静默改用内存或 SQLite。

### 事务与并发

| 操作     | 共同提交的内容                           |
| -------- | ---------------------------------------- |
| 准入     | Run、用户消息、接受事件、幂等约束        |
| 完成     | 最终回答、成功状态、完成事件、断点失效   |
| 新问替代 | 旧 Run 取消、断点失效、新 Run 与消息     |
| 暂停确认 | 检查有效断点后提交暂停与事件             |
| 恢复准入 | 校验暂停和页面，增加 attempt、状态和事件 |

框架断点写入与最终确认暂停是相邻步骤，不是天然同一事务；通过保存结果校验、执行围栏、启动对账处理。

- 幂等：唯一范围 `(user_id, conversation_id, idempotency_key)`，摘要检测同键异参。
- 会话互斥：部分唯一索引保证一个非终态 Run，包含 paused；新问走替代事务。
- 状态条件更新：校验预期状态与 attempt，维护递增 version。
- 并发继续：事务锁定 Run 后做 paused → resuming，最多启动一个恢复执行。
- 迟到写入：消息/事件/断点绑定 Run 和 attempt，旧回调不能污染新任务或激活失效断点。
- 消息去重：工具调用 ID/调用组派生稳定来源键，数据库按 Run 与来源键去重。
- 单实例：API 持有 PostgreSQL session advisory lock，第二个实例拒绝启动。

这些措施保障当前单实例和数据库边界，没有跨服务分布式事务、跨实例调度或任意外部写入的“恰好一次”保证。

## 安全、预算与诊断

### 安全措施

bcrypt 密码摘要；Session/CSRF/page_token 库存摘要；HttpOnly、SameSite=Lax Cookie；HTTPS 使用 Secure，本机开发允许 HTTP。认证写请求检查 X-CSRF-Token，有 Origin 头时校验配置来源。Scope 限定资源归属，限制跨用户历史、任务、断点与 SSE 访问。

模型 Key 仅服务端环境配置，不发前端或写入断点；只读工具不接受用户 ID、任意 SQL 或任意 URL；日志/错误使用安全代码，不透传模型原始错误、密码、Cookie 或断点 payload；.env 只解析白名单，不执行内容。

当前没有 JWT/Refresh Token、组织租户或 RBAC 后台。

### 默认预算

| 项目              | 默认值与配置                                   |
| ----------------- | ---------------------------------------------- |
| 模型迭代          | 8 次，MAX_ITERATIONS                           |
| 活动执行          | 120 秒，RUN_SECONDS；暂停不计时，恢复重新计时  |
| 工具              | 10 秒，TOOL_SECONDS                            |
| 心跳/页面租约     | 5/20 秒，HEARTBEAT_SECONDS/LEASE_SECONDS       |
| 暂停等待          | 15 秒，PAUSE_SECONDS                           |
| 会话非终态        | 1 个，数据库约束                               |
| 用户/进程活动执行 | 3/8 个，当前代码常量                           |
| 上下文/输出       | 约 32,000/256,000 字节                         |
| 部分输出保存      | 增量到达时检查约 250ms 或新增 4KB，退出时刷新  |
| SSE               | 每订阅 64 条缓冲，每 Run 8 个订阅，进程 128 个 |
| 注册/登录         | IP 共享每分钟 10 次，进程内限流                |

没有 Token/费用账单或独立模型请求 RPM 配额；模型资源主要由并发、时限和迭代控制。

### 诊断入口

- `/health/live` 为存活，`/health/ready` 为数据库就绪，不代表模型已调用成功。
- 登录后的 `/api/v1/metrics` 返回活动、启动、成功、失败、暂停、取消和累计耗时等进程 JSON 计数。
- 日志关联 request_id、run_id、attempt、状态、工具耗时和安全错误代码。

没有 Prometheus 导出器、OpenTelemetry、集中日志或告警系统。

## 实际目录与规划差异

```text
agent-platform/
├── backend/
│   ├── cmd/server/main.go             # 进程入口
│   ├── internal/
│   │   ├── bootstrap/                 # 构造、注入与注册
│   │   ├── config/                    # 环境变量与预算
│   │   ├── router/                    # Gin 路由
│   │   ├── middleware/                # 认证、CSRF、日志、来源、限流
│   │   ├── handler/                   # API、生命周期、SSE
│   │   ├── application/chat/          # 聊天用例协调
│   │   ├── service/
│   │   │   ├── auth/                  # 注册、登录、认证、登出
│   │   │   ├── user/                  # 当前用户
│   │   │   ├── conversation/          # 会话、访问与历史
│   │   │   └── settings/              # 设置规则与读写
│   │   ├── agent/
│   │   │   ├── contracts/             # Agent / Execution / Event
│   │   │   ├── manager/               # Run、页面租约、事件、指标
│   │   │   ├── agents/
│   │   │   │   ├── general/           # 已实现 Eino 聊天/恢复适配
│   │   │   │   └── travel/            # 仅接入说明，无执行器
│   │   │   ├── memory/                # ContextBuilder
│   │   │   ├── ports/                 # 存储契约
│   │   │   └── tools/                 # 预留，无通用工具管理器
│   │   ├── agenttools/                # 设置工具与事件桥接
│   │   ├── repository/postgres/       # SQL、事务、围栏、对账
│   │   ├── client/llm/                # 模型客户端
│   │   └── model/                     # 平台实体与存储输入
│   ├── migrations/                    # 版本化 SQL 与 embed
│   ├── tests/integration/             # 真实 PostgreSQL / HTTP 测试
│   ├── go.mod / go.sum
│   └── Dockerfile
├── frontend/
│   ├── src/
│   │   ├── api/                       # API / SSE / 类型
│   │   ├── components/                # 登录、入口、历史、任务按钮
│   │   ├── hooks/                     # 页面租约与 Run 订阅
│   │   └── App.tsx                    # 工作区协调和渲染
│   ├── package.json / package-lock.json
│   ├── Dockerfile
│   └── nginx.conf
├── scripts/                           # 启动、验收、模型冒烟
├── deploy/postgres/init-app.sh         # Compose 应用角色初始化
├── .github/workflows/ci.yml            # Linux / 前端 CI
├── openspec/specs/                     # 正式主规格
├── openspec/changes/archive/           # MVP 归档
├── docs/                              # 设计、运行、自审、证据
├── .env.example                       # 无真实凭据的配置模板
├── compose.yaml                       # 可选编排，未实测
└── THIRD_PARTY_NOTICES.md
```

只有 backend 一个 Go module，前端是独立 npm 项目。接口定义在消费方或 ports，bootstrap 注入 Repository；Repository 不反向依赖上层执行器，Eino 类型主要封装在 Agent 和工具适配内。

### 与最初设想的差异

| 原规划                             | 实际落地                                              |
| ---------------------------------- | ----------------------------------------------------- |
| 独立 AgentRunner/runtime           | 放在 agents/general/runner.go，封装 Eino              |
| manager/run_manager.go             | 职责合并在 manager.go、lifecycle.go，无重复管理结构体 |
| 多个 Store 文件                    | 核心契约在 ports/stores.go，其他接口靠近消费方        |
| 工具注册中心/执行器                | 设置工具直接注册 Eino，通用工具管理目录仅预留         |
| TravelAdapter/Python 服务          | 只有说明与不可用入口                                  |
| credential/modelconfig/mcp Service | 未实现，没有空 Service 冒充功能                       |
| Redis、MCP、RAG、travel Client     | 未引入，目前为 PostgreSQL 和模型 Client               |

建议从 [bootstrap](backend/internal/bootstrap/bootstrap.go) → [Chat Application](backend/internal/application/chat/service.go) → [Manager](backend/internal/agent/manager/manager.go) → [Runner](backend/internal/agent/agents/general/runner.go) → [Repository](backend/internal/repository/postgres/) → [集成测试](backend/tests/integration/) 阅读。

## 本地运行

### 1. 前置与获取

需要 Go 1.26.0+、Node.js 24/npm、PostgreSQL 17 和 PowerShell 7。聊天需要支持 Chat Completions、流式响应、工具调用的模型。当前不要求 Docker、Redis、Python。

本机验收是 Go 1.27.2、Node 24.20.0、PostgreSQL 17.11，CI 为 Go 1.26.x。

```powershell
git clone --branch feat/agent-platform-mvp https://github.com/yzy703050-oss/-agent-platform.git agent-platform
cd agent-platform

# 新环境创建，已有文件保留
if (-not (Test-Path -LiteralPath .env)) {
    Copy-Item .env.example .env
}
```

### 2. 配置

根目录 .env 最小形态如下，值均为占位符：

```dotenv
DB_DSN=postgres://agent_platform:REPLACE_WITH_LOCAL_PASSWORD@127.0.0.1:5432/agent_platform?sslmode=disable
TEST_DATABASE_URL=postgres://agent_platform_test:REPLACE_WITH_TEST_PASSWORD@127.0.0.1:5432/agent_platform_test?sslmode=disable
HTTP_ADDR=127.0.0.1:8080
PUBLIC_ORIGIN=http://127.0.0.1:5173
COOKIE_SECURE=false
LLM_BASE_URL=https://YOUR_PROVIDER/v1
LLM_MODEL=YOUR_MODEL_ID
LLM_API_KEY=YOUR_LOCAL_KEY
```

Base URL 是 API 前缀，不是浏览器地址或完整 /chat/completions 地址。修改后重启 API。模型留空时基础功能仍可用，聊天明确不可用；只有实际调用才能验证凭据和协议。

开发库和测试库分开，连接密码含特殊字符需 URL 编码。真实 .env、管理凭据、.cache 不提交 Git。

### 3. 数据库

验收电脑已安装 PostgreSQL，见 [本地记录](docs/local-postgres.md)；新克隆不会自动安装数据库。新环境由管理员进入 psql，可替换占位符后初始化：

```sql
CREATE ROLE agent_platform LOGIN PASSWORD 'REPLACE_WITH_LOCAL_PASSWORD';
CREATE DATABASE agent_platform OWNER agent_platform;
CREATE ROLE agent_platform_test LOGIN PASSWORD 'REPLACE_WITH_TEST_PASSWORD';
CREATE DATABASE agent_platform_test OWNER agent_platform_test;
```

不要对已有库重复执行。应用账号需要在自己的库中迁移表结构，不需超级用户。Go 启动只自动迁移表，不创建库或账号。

### 4. 启动

根目录第一个终端：

```powershell
pwsh -NoProfile -File .\scripts\dev-server.ps1
```

第二个终端：

```powershell
Set-Location frontend
npm ci
Set-Location ..
pwsh -NoProfile -File .\scripts\dev-frontend.ps1
```

访问 <http://127.0.0.1:5173>，API 默认 <http://127.0.0.1:8080>；Vite 代理 /api、/health。浏览器来源与 PUBLIC_ORIGIN 保持一致，不混用 localhost 和 127.0.0.1。

脚本读取 .env，直接 go run 时程序只读进程环境，不自行加载 .env。停止用对应终端 Ctrl+C。相同数据库上第二个 API 因单实例锁拒绝启动属于预期行为。

### 5. 环境变量

| 变量                                              | 默认或必填条件        | 用途                            |
| ------------------------------------------------- | --------------------- | ------------------------------- |
| DB_DSN                                            | 后端必填              | 运行库                          |
| TEST_DATABASE_URL                                 | 完整验收必填          | 独立测试库，正常 API 运行不需要 |
| HTTP_ADDR                                         | 127.0.0.1:8080        | 监听地址                        |
| PUBLIC_ORIGIN                                     | http://127.0.0.1:5173 | 浏览器来源                      |
| COOKIE_SECURE                                     | 按来源是否 HTTPS 推导 | 模板显式本地 false              |
| LLM_BASE_URL / LLM_MODEL / LLM_API_KEY            | 聊天必填              | 服务端模型配置                  |
| MAX_ITERATIONS / RUN_SECONDS / TOOL_SECONDS       | 8 / 120 / 10          | 执行预算                        |
| HEARTBEAT_SECONDS / LEASE_SECONDS / PAUSE_SECONDS | 5 / 20 / 15           | 在线和暂停预算，租约须大于心跳  |
| POSTGRES_PASSWORD                                 | 仅 Compose            | 初始化管理员密码                |
| APP_DATABASE_PASSWORD                             | 仅 Compose            | 与管理员不同的应用角色密码      |

### 6. 可选容器路径

已提供 Dockerfile、Nginx 同源代理、初始化脚本与 Compose，**尚未实际构建或运行验收**。具备 Docker 的环境可在 .env 配置不同且 URL 安全的两个数据库密码及模型后尝试：

```powershell
docker compose up --build
```

设计访问地址 <http://127.0.0.1:8088>；仅发布本机前端端口，后端和库在编排网络内，数据使用命名卷，与原生 PostgreSQL 独立。

这不是已验证生产方案，尚无公网 HTTPS、自动发布、备份演练或高可用配置。

## API 与事件协议

除健康检查外前缀为 /api/v1。注册和登录无需已有 Session，其余需认证，写接口需 CSRF。

| 方法         | 路径                                                | 作用                      |
| ------------ | --------------------------------------------------- | ------------------------- |
| POST         | /auth/register、/auth/login、/auth/logout           | 身份入口                  |
| GET          | /me                                                 | 当前用户                  |
| GET/PATCH    | /settings                                           | 设置                      |
| GET/POST     | /conversations                                      | 游标列表/按入口创建       |
| PATCH/DELETE | /conversations/:id                                  | 重命名/受控删除           |
| GET          | /conversations/:id/messages                         | ordinal 分页历史          |
| GET          | /agents                                             | 可用状态                  |
| POST         | /pages                                              | 独立页面租约              |
| POST         | /pages/:id/heartbeat、/pages/:id/leave              | 心跳/离开                 |
| POST         | /conversations/:id/runs                             | 幂等提交                  |
| GET          | /runs/:id                                           | 状态、attempt、输出、错误 |
| GET          | /runs/:id/events                                    | SSE                       |
| POST         | /runs/:id/pause、/runs/:id/resume、/runs/:id/cancel | 暂停/继续/结束            |
| GET          | /metrics                                            | 进程基础指标              |

有非终态任务不能删除会话。实际注册见 [router.go](backend/internal/router/router.go)。

提交 JSON 示例：

```json
{
  "content": "请调用 get_my_settings 读取我的主题和回复偏好。",
  "page_id": "SERVER_ISSUED_PAGE_ID"
}
```

还需 Session Cookie、X-CSRF-Token、X-Page-Token、Idempotency-Key。页面令牌不放 URL；封装见 [client.ts](frontend/src/api/client.ts)。

SSE 字段包括 run_id、attempt、type、seq、payload。以下只是格式示例，不是验收响应：

```text
id: 12
event: run.completed
data: {"run_id":"RUN_ID","attempt":1,"type":"run.completed","seq":12,"payload":{}}
```

message.delta 为文字；tool.started/completed/failed 为工具状态。持久事件有 seq 和 SSE id，临时文字无可重放的持久事件 ID，重连支持 Last-Event-ID 或 after。前端按 Run/attempt 拒绝旧增量，持久状态以查询为准。

错误结构为 `{code, message, request_id}`：

| 错误或现象                          | 排查                                     |
| ----------------------------------- | ---------------------------------------- |
| unauthenticated                     | Session 失效，重新登录                   |
| csrf_rejected / origin_rejected     | 检查 CSRF、PUBLIC_ORIGIN                 |
| auth_unavailable                    | 认证存储故障，不能等同密码错误           |
| run_conflict                        | 同会话已有冲突任务                       |
| idempotency_conflict                | 同键异参，重试保留键，新问使用新键       |
| run_quota_exceeded                  | 活动执行或订阅达上限                     |
| model_unavailable                   | 检查服务端模型配置/初始化，修改后重启    |
| agent_unavailable                   | 目标未接入，当前差旅如此                 |
| checkpoint_incompatible             | 当前模型/工具/框架不能恢复旧断点         |
| another platform instance is active | 原实例持有锁，确认进程，不绕过锁         |
| interrupted，无继续入口             | 没有可证明安全的断点，查看历史后发新问题 |

关页、断网、结束任务不等于外部操作回滚；当前没有外部写工具。

## 验证与演示

### 基线证据

| 检查         | 结果                                                           |
| ------------ | -------------------------------------------------------------- |
| Go           | 84 PASS、0 FAIL、0 SKIP，含真实 Eino 可控模型、PostgreSQL/HTTP |
| vet/构建     | 本机与 Linux 通过                                              |
| 前端         | 13 项组件测试、TypeScript、生产构建通过                        |
| Linux race   | 实际 PostgreSQL 下 go test -race ./... 通过                    |
| 真实模型     | 回答、设置工具、安全边界暂停恢复通过                           |
| 浏览器       | 切换、离开暂停、重开不自动执行、继续、新问替代                 |
| OpenSpec     | 5 份规格、22 个 Requirement、36 个 Scenario 核对并归档         |
| Windows race | 本机工具链失败，无通过证据                                     |
| Docker       | 仅配置与语法检查，未实际运行                                   |

数量是验收基线，不是覆盖率或性能指标。可控模型测试不代替真实 API 验收。

### 本机自动化

```powershell
pwsh -NoProfile -File .\scripts\verify.ps1
```

脚本强制要求 TEST_DATABASE_URL、不允许数据库检查跳过，执行 Go 测试、vet、构建、npm ci、前端测试/构建，报告在 .cache/reports/。

Windows 构建前停止使用 .cache/bin/server-verified.exe 的已确认项目 API；npm ci 前停止 Vite，释放文件锁。依赖已按锁文件安装可加 -SkipNpmInstall。

普通 go test 缺测试库时显式 skip，不能算完整发布验收。集成测试在独立测试库创建随机 schema，结束清理各自 schema，不清空开发库。

### 真实模型冒烟

API 已运行并加载配置后执行：

```powershell
pwsh -NoProfile -File .\scripts\model-smoke.ps1
pwsh -NoProfile -File .\scripts\model-smoke.ps1 -ExerciseRecovery
```

创建合成账号/会话并调用实际模型，会消耗服务商额度；仅输出脱敏状态，不输出 Key、Cookie、页面令牌或模型全文。恢复使用短工具步骤安全边界，任务先完成或无断点会明确失败。长流失败保留在 [验收记录](docs/real-model-acceptance.md)，不假装任意位置恢复。

### CI 与演示

[ci.yml](.github/workflows/ci.yml) 在 push/pull request 时运行：后端 PostgreSQL service → race → vet → build；前端 npm ci → Vitest → TypeScript/Vite。使用合成库，不上传本地模型 Key，不自动执行付费模型检查。

演示建议：注册登录和设置 → 调设置工具 → 多步骤任务中切换历史/入口 → 安全暂停或离开检测 → 重开只展示、点击继续观察 attempt → 暂停后新问替代 → 差旅明确未接入。

模型是否提及旧标记不能独立证明上下文隔离，输入排除、旧写拒绝与事务回滚由自动测试/数据库核对证明。

## 尚未实现和未验证

### 产品能力

| 方向      | 未做部分                                                           |
| --------- | ------------------------------------------------------------------ |
| 差旅      | Python/FastAPI、远程 Client、TravelAdapter、行程业务/工具/恢复协议 |
| 多 Agent  | Query 路由、子 Agent、父子 Run、委派和协作                         |
| 动态管理  | Agent 后台、热更新、发布版本和动态注册                             |
| MCP       | 服务管理、客户端、工具发现                                         |
| RAG       | 上传、解析、切分、Embedding、向量库、检索                          |
| 长期记忆  | 提取、摘要、画像、跨会话、遗忘                                     |
| 用户模型  | 凭据中心、每用户模型、多供应商切换；目前只有服务端环境配置         |
| 审批/写入 | 人工审批 UI、订票/下单/付款等副作用工具                            |
| 本地执行  | Shell、文件工具、执行沙箱                                          |
| 多模态    | 图片、语音、附件和富文本编辑                                       |

### 生产能力

未做多实例、分布式任务租约、队列/Worker 调度、故障接管、自动恢复执行；未做 Redis、跨节点事件路由和全局配额；没有 Token/费用账单、Prometheus/OpenTelemetry、集中告警、SLO、压测与容量报告。

也没有组织租户/RBAC、账号找回/第三方登录、生产 HTTPS/自动发布、备份恢复与迁移回滚演练、长期消息/事件/断点自动清理。当前完成的是单实例 MVP，不等于生产高可用平台完成。

### 已知边界

Eino 固定 beta 版本，不保证跨不兼容版本迁移；强制截流不保证恢复；关页有延迟；外部写入结果未知与幂等还没有真实业务实现。App.tsx 职责偏多，可后续拆分。Docker 未实测，Windows 本机 race 工具链仍有问题。

## 后续扩展与正式文档

### 建议顺序

以下为建议，不代表已经完成或未经确认的新需求：

1. 重构接入差旅：先定执行/事件/身份/恢复协议，再实现独立入口的 TravelAdapter。
2. 打通数据边界：Python 不能 import Go Repository，可按业务定义受认证内部存储 API；该 API 当前不存在。
3. 增加主 Agent 委派：独立差旅稳定后再做 Query 路由、父子 Run、取消传播和上下文传递。
4. 按需求逐项建设 MCP、RAG、长期记忆，再决定引入组件。
5. 有规模需求后建设调度、全局配额、跨节点事件与故障接管；单实例锁不是多实例方案。

Python 差旅可以保留自己的内核/断点格式，不要求理解 Eino gob payload；平台统一权限、Run、事件和数据边界。

### 正式文档

README 描述实际实现，不另立规格。行为以主规格为准，设计与过程在归档中可追溯。

| 文档                                                                             | 内容                     |
| -------------------------------------------------------------------------------- | ------------------------ |
| [主规格](openspec/specs/)                                                        | 正式行为与场景           |
| [提案](openspec/changes/archive/2026-10-23-build-agent-platform-mvp/proposal.md) | 目标、范围和影响         |
| [设计](openspec/changes/archive/2026-10-23-build-agent-platform-mvp/design.md)   | 边界、接口、数据与取舍   |
| [任务](openspec/changes/archive/2026-10-23-build-agent-platform-mvp/tasks.md)    | 完成证据                 |
| [实施计划](docs/superpowers/plans/2026-10-22-agent-platform-mvp.md)              | TDD 与验证步骤           |
| [运行手册](docs/runbook.md)                                                      | 启动和故障处理           |
| [验证记录](docs/verification.md)                                                 | 本机、模型、浏览器、CI   |
| [场景矩阵](docs/spec-verification.md)                                            | 36 个 Scenario 的证据    |
| [代码自审](docs/code-review.md)                                                  | 发现、修复、暂缓项       |
| [执行记录](docs/execution-ledger.md)                                             | 分阶段决策与失败修复历史 |

MVP 为单代理实施、作者整体自审，没有声称独立第三方评审。未启用专用 OpenSpec verify 工作流，采用人工逐项一致性核对，不把 validate 当作代码测试。

执行恢复参考 CloudWeGo [Eino](https://github.com/cloudwego/eino) 和 [chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)。依赖许可证与来源见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，平台自有代码许可证尚未单独选定。
