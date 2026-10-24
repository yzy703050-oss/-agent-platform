# 小豆包 · Go Agent Platform

一个突出 Go 后端工程能力的 Agent 平台。React 提供聊天工作台，Gin 提供 HTTP/SSE，Eino 执行模型与工具循环，PostgreSQL 统一保存用户、会话、任务、行程、证据、记忆及断点。

当前包含两个独立入口：**通用聊天**和**Go 原生差旅助手**。点击入口新建会话，历史保持原 Agent 归属。主 Agent 自动委派差旅尚未实现。

> 原 Python 差旅助手的原文提示、工具契约、Main/Verifier/Summary 链路、校正及停止机制已迁入现有分层。2026-10-24 最后一轮真实模型＋模拟工具＋PostgreSQL 验收为 12/12；此前失败轮次也完整保留。Linux race 和本次归档状态以 [最新验收记录](docs/travel-source-fidelity-verification.md) 为准，旧 MVP 的 CI 不作为当前提交的证据。

## 已实现的能力

| 能力 | 实现情况 |
| --- | --- |
| 用户与设置 | 注册、登录、登出、当前用户、主题和回复偏好；服务端 Session、HttpOnly Cookie、CSRF 和来源检查 |
| 会话历史 | 创建、重命名、受控删除、游标分页；两个入口共用历史和权限体系 |
| 普通聊天 | 真实 OpenAI 兼容模型、只读设置工具、流式输出、Eino 工具循环 |
| 差旅行程 | 多段与返程、日期和住宿、活动、硬软约束、按分计算预算、增量修改与依赖失效 |
| 外部查询 | 聚合数据车次、高德酒店地点、wttr.in 天气客户端；可显式切换带标识的演示数据 |
| 候选选择 | 核验身份、会话、查询、行程、修订、候选 ID 与时效；酒店地点没有房价/库存保证 |
| 政策检索 | 通过官方 Go MCP SDK 调用已有 Python RAG 服务；保留来源，区分未命中、失败和适用条件不足 |
| 差旅记忆 | 带来源的偏好修订和撤回、有效版本回退、摘要来源检查、保守的低频分析 |
| 执行恢复 | 同一 Run 的安全点暂停、兼容断点、手动继续、attempt 围栏和业务操作回执 |
| 新问题替代 | 取消暂停旧任务、使断点失效；历史可看，但旧消息、工具结果、偏好和混合摘要不进入后续自动上下文 |
| 诊断 | request_id/run_id/attempt、工具耗时、健康检查、基础指标、版本化迁移及 CI |

**真实依赖证据：**车次接口已返回有效候选，高德接口已返回酒店地点；真实模型已完成保存行程及后续修订。Go 通过现有 RAG 的 stdio MCP、本地 BGE 和独立 `agent_platform_demo` 集合检索到自编演示政策片段。浏览器与当前提交的最终 CI 状态以验收文档为准。

## 技术栈与部署边界

- Go 1.26+、Gin、pgx/v5、PostgreSQL 17。
- Eino 固定 `v0.9.0-beta.1`，沿用 ChatModelAgent、Handlers、TurnLoop 与 Checkpoint。
- React、TypeScript、Vite、Vitest。
- 官方 MCP Go SDK 固定版本；现有 RAG 保持独立 Python 服务。
- 当前为模块化单体、单实例 API。Redis、消息队列、多实例调度和动态 Agent 发布均未引入。
- 活动执行句柄、事件通知与部分配额位于 Go 进程；正式状态、回执和断点位于 PostgreSQL。
- Dockerfile/Compose 已提供，尚未在本机运行验证。本地不要求 Docker。

精确依赖见 [go.mod](backend/go.mod) 和 [package.json](frontend/package.json)。

## 整体分层

```mermaid
flowchart TD
    UI[React 工作台] --> HTTP[Router / Middleware]
    HTTP --> H[Handler]
    H --> S[传统 Service：用户 / 会话 / 设置]
    H --> APP[Application / Chat：用例协调]
    APP --> S
    APP --> M[AgentManager：注册选择 / Run / 配额 / 页面归属]
    M --> G[GeneralAgent]
    M --> T[TravelAgent]
    G --> E[共享 Eino Runner / Checkpoint 适配]
    T --> E
    E --> K[Eino ChatModelAgent / Handlers / 工具循环]
    K --> L[模型 Client]
    K --> TOOLS[业务工具适配]
    K --> MEM[Memory：有效来源 / 预算 / 摘要]
    TOOLS --> TS[Trip / TravelPreference Service]
    TOOLS --> S
    TS --> R[Repository / PostgreSQL]
    S --> R
    M --> R
    E --> R
    MEM --> R
    TS --> C[车次 / 酒店 / 天气 / MCP Client]
    C --> EXT[外部 API / 既有 RAG]
```

### Service 与 Agent 是并列系统

传统 Service 管确定性业务：认证、权限、会话规则、设置、行程校验、偏好修订等。Agent 管模型与工具的执行编排。聊天经 Application 协调会话权限和 AgentManager；普通设置接口直接进入对应 Service。

Repository 是统一的数据访问实现层，**不表示所有调用都可以绕过 Service**：

- 行程与偏好变更：Tool → Service → Repository。
- Run、事件、消息、Checkpoint：平台组件 → 消费方 Store 接口 → Repository。
- 外部模型、供应方和 MCP：对应 Client；网络请求不放在数据库事务里。
- bootstrap 创建并注入组件，Repository 不反向依赖具体 Agent。

### Agent 内部职责

| 模块 | 职责 |
| --- | --- |
| `agent/contracts` | Agent、Execution、事件和执行限制；不包含差旅行程规则 |
| `agent/manager` | 静态注册、按会话选择 Agent、Run 状态、配额、租约、通知和恢复对账 |
| `agents/general` | 普通聊天配置、设置工具、平台消息转换 |
| `agents/travel` | 同一 Eino 内的 Main/Verifier/Summary、原文提示、政策/偏好/压缩辅助角色、决策与恢复；无独立 adapter 层 |
| `agent/einoruntime` | 共用 Eino TurnLoop、停止确认、断点包络与 Repository 适配 |
| `agent/memory` | 授权历史、完整 Run 裁剪、偏好注入、来源受限摘要及低频维护 |
| `agenttools/travel` | 类型化工具与 Service 桥接；可信身份来自服务端，不来自模型参数 |
| `service/trip` | 行程规则、证据选择、查询编排、政策适用检查及只读时效核验 |
| `repository/postgres` | SQL、锁、事务、唯一约束、修订、回执与执行围栏 |

没有再造一套并列 ReAct 循环，也没有复制 Python 的身份、SQLite 或任务系统。

## 实际目录

```text
backend/
  cmd/server/main.go
  internal/
    bootstrap/                 # 组装、注入、注册
    config/                    # 配置及预算
    router/ middleware/        # Gin、认证、CSRF、来源、日志、限流
    handler/                   # HTTP、SSE、任务控制
    application/chat/          # 聊天用例协调
    service/
      auth/ user/ conversation/ settings/
      trip/                    # 规则、查询、选择、政策、时效检查
      travelpreference/        # 偏好保存与撤回
    agent/
      contracts/ manager/ ports/
      einoruntime/             # General 与 Travel 共用
      agents/general/
      agents/travel/           # Go 原生差旅，非 Python 远程适配器
      memory/
      tools/                   # 通用动态工具管理仍未实现
    agenttools/
      settings_tool.go
      travel/                  # 原生差旅工具与 Service 的类型化桥接
    repository/postgres/
    client/llm/ train/ hotel/ weather/ mcp/ providerhttp/
    model/                     # 无 Eino 依赖的业务与平台类型
  migrations/                  # 001–005 增量迁移
  tests/integration/           # 独立测试库、随机 schema
frontend/src/
  api/ components/ hooks/ App.tsx
scripts/                       # 启动、配置加载、验证
openspec/                      # 正式需求与当前变更
  changes/preserve-travel-source-flow/
docs/                          # 计划、职责、自审、验收与运行说明
```

只有一个后端 Go module。原 Python 差旅与 RAG 项目没有复制进本仓库；不导入其旧行程、历史、偏好或断点，也不删除原项目数据。

## 关键调用流程

### 一次普通聊天或差旅请求

1. React 创建固定 `agent_type` 的会话，提交带页面归属和幂等键的请求。
2. Handler 完成 HTTP/CSRF/页面校验；Application 通过 ConversationService 检查会话权限。
3. Manager 按会话类型选 Agent，检查会话互斥、用户和进程配额，以事务接纳 Run。
4. Memory 查询允许来源的历史，按完整 Run 组装上下文。
5. 具体 Agent 创建 Execution；Manager 转为 running 后才调用 Start。
6. Eino 执行模型与工具循环；平台保存工具消息、事件及最终结果。

`NewExecution` 也用于启动时的恢复兼容性探测，因此构造阶段不调用模型、查询供应方或写差旅业务。

### 一次差旅规划

1. 模型理解用户条件，调用 `write_trip(propose)`。
2. TripService 校验路线、返程、日期、住宿、活动、硬软约束和金额；缺必要条件保留追问状态。
3. Repository 在同一事务保存行程修订及操作回执。
4. 工具自动执行规划返回的查询需求；独立查询也复用相同 Service。
5. 模型只能用实际返回的查询 ID、结果修订及候选 ID 执行 `select`。
6. Service 和事务共同核验归属、绑定、时效及 expected_revision，保存下一修订。
7. 最终行程事实从实际保存结果生成，明确未知价格、未选择候选和未核验政策；不声称已付款、出票或订房。

修改活动不会直接丢弃无关车次；修改日期、路线或相关条件会使受影响选择失效。硬约束不能由模型暗中降为软约束。

### 工具与查询

| 工具 | 作用 |
| --- | --- |
| `read` | 分页检索行程/会话历史，读取完整行程；先过滤失效任务来源 |
| `query` | 车次、酒店地点、天气；未配置的网页查询明确 unavailable |
| `write_trip` | propose / amend / select，写入必须经 Service 与事务 |
| `query_policy` | 从既有 RAG MCP 获得完整片段和来源；命中不等于合规 |
| `write_memory` | 明确新增/替换/撤回偏好，或按用户要求重分析 |
| `request_replan` | Verifier 独立交接回 Main 修复全局遗漏，不通过 AgentManager |

原生查询采用原版规范化键、物理结果缓存与五项候选投影、最多三个并发和一小时证据时效；引用仍绑定当前任务修订。Client 单独负责端点并发、熔断及最多额外两次网络重试，Service 不叠加重试。普通工具 I/O 受 ToolTimeout 限制；政策/偏好 LLM 作为辅助角色，保留自身请求期限并共享 Run 截止时间。来源不明、价格未知或查询失败不生成事实。

酒店接口是高德地点搜索，不具备房型、房价、空房或预订能力。网页检索供应方尚未确定。政策接口固定调用已核验的 `query_policy_evidence`；执行命令、目录和集合由本地配置指定，模型不能更改。

## 暂停与恢复

平台恢复与 Agent 恢复承担不同职责：平台管理用户、Run、页面归属、attempt 和外部状态；Eino 恢复模型/工具循环内部的执行位置。两者通过兼容指纹、Checkpoint、决策日志和业务回执衔接。

- 页面内切换会话或入口，原任务继续。
- 关闭发起页面后，通过 leave 通知或页面租约到期请求安全暂停；SSE 断开本身不代表页面关闭。
- 重开只恢复显示。用户点击继续，才恢复同一 Run，增加 attempt 并绑定新页面。
- 只有执行停止且兼容断点保存成功，才能显示 paused。
- 优雅停止升级为强制取消、断点失败或业务结果未知时，不能虚报可继续。
- 继续前检查引用证据；过期选择不能作为当前事实，明确要求重新查询选择。

完整模型决策在工具之前保存，逻辑 operation ID 不含 attempt。业务修订与回执原子提交；旧断点重放首先读取原决策和工具观察，已成功写入返回原回执。同标识不同参数冲突；结果 unknown 停止自动重放并提示待核实。

安全点停止期间可记录已经在途的模型决策元数据，业务写入仍必须处于 running。启动对账和兼容性探测不自动恢复执行。不保证任意 Token 位置无损续写。

## 记忆和新任务隔离

显示历史与模型上下文是不同查询。暂停任务被新问题替代后，历史保留，旧断点失效；旧任务消息、输出、工具结果、偏好修订及包含旧来源的摘要均排除。更早仍有效的信息可以继续使用，不清空整个用户的有效历史。

普通聊天保留原 ContextBuilder 策略。差旅使用原版 20,000 Token 上下文预算估计、80% 压缩触发和最多三个完整前缀压缩，保留当前轮和完整工具配对。review 只投影当前计划与必要证据，引用去重已出现在快照中的政策、候选和初始提案；完整审计仍保存。当前请求本身超预算会明确拒绝。

低频维护只处理允许来源：新增至少五个有效来源 Run 后才分析，保守地识别多个独立用户消息重复出现的白名单完整偏好句，写 inferred 修订；不以推断覆盖明确值或复活撤回。摘要只提取带回执的行程路线、日期和修订并记录全部来源，不携带历史候选作为新证据，不额外启动后台模型。

## 数据库与并发一致性

迁移由 Go embed 分发，启动时按版本执行：001 基础表，002 消息去重，003 已有输出，004 差旅业务，005 原生偏好批量回执/来源与持久化查询作业。旧迁移不改写；005 不执行隐式有损降级。

004 包括行程、不可变修订、查询证据、已接受决策、动作日志、操作回执、偏好修订和来源摘要。归属、修订和操作键使用显式列、外键及唯一约束，业务快照使用 JSONB。

统一锁顺序为会话 → Run → 业务资源。事务校验当前 attempt、状态、权限、expected_revision 与候选；模型和外部 HTTP 不占据业务事务。暂停/取消先取得状态迁移后，迟到业务写入被拒绝；业务若先提交，则通过回执衔接。

当前实例通过 PostgreSQL advisory lock 防止误启动两个 API。每会话一个非终态任务、每用户最多三个活动执行、进程最多八个。数据库不可用时不回退到内存假成功。

## 本地配置与运行

`TRAVEL_QUERY_MODE=live`（默认）使用真实查询；显式设为 `demo` 后，车次、酒店地点、天气使用确定性模拟结果。证据和选中候选保存 `is_demo`，界面与最终回复标注“演示数据”；真实模式失败不会自动回退。两种模式缓存隔离，酒店房价/库存仍未知，行程事务、鉴权、断点和模型调用仍是真实实现。

自编文档见 [演示差旅政策](docs/fixtures/demo-travel-policy.txt)，其中主体、额度、流程均为虚构。只导入独立 `agent_platform_demo` 集合；政策结果标明“演示政策”，不用于正式报销判断。`RAG_SETTINGS_PATH` 可指定已有 RAG 的绝对配置路径，本机使用其 `config/travel_policy.yaml` 的本地 BGE，不需要另提供嵌入 API Key。

需要 Go 1.26+、Node.js 24、PostgreSQL 17、PowerShell 7。已有 `.env` 请保留；新环境才复制 [.env.example](.env.example)。

```dotenv
DB_DSN=postgres://USER:PASSWORD@127.0.0.1:5432/agent_platform?sslmode=disable
TEST_DATABASE_URL=postgres://TEST_USER:PASSWORD@127.0.0.1:5432/agent_platform_test?sslmode=disable
LLM_BASE_URL=https://YOUR_PROVIDER/v1
LLM_MODEL=YOUR_MODEL_ID
LLM_API_KEY=LOCAL_ONLY
TRAIN_API_KEY=LOCAL_ONLY
AMAP_API_KEY=LOCAL_ONLY
RAG_MCP_COMMAND=ABSOLUTE_PATH_TO_PYTHON
RAG_MCP_DIR=ABSOLUTE_PATH_TO_EXISTING_RAG_PROJECT
RAG_COLLECTION=YOUR_POLICY_COLLECTION
RAG_MCP_ARGS=["-m","src.mcp_server.server"]
```

模型需要 Chat Completions 和工具调用。两个 Agent 当前共享服务端模型配置。缺少配置时显示不可用；填写了配置不代表远端凭据必然有效，必须以实际调用验证。

RAG 为可选外部能力，缺失或检索失败时保留政策未核验。其 Python 运行环境由原 RAG 项目维护，Go 不重建向量索引。连接子进程不继承平台数据库或模型密钥。

两个终端分别执行：

```powershell
pwsh -NoProfile -File .\scripts\dev-server.ps1
```

```powershell
cd frontend
npm ci
cd ..
pwsh -NoProfile -File .\scripts\dev-frontend.ps1
```

打开 [本地工作台](http://127.0.0.1:5173)。API 为 127.0.0.1:8080，Vite 提供同源代理。配置在进程启动时加载，修改后需重启 API。数据库、模型及供应方密钥只放在本地 `.env`，不要提交。

首次启动只追加已版本化的迁移，不创建数据库或用户，不执行 down/清空。开发库与测试库必须分开；本机数据库信息见 [运行说明](docs/runbook.md) 和 [安装记录](docs/local-postgres.md)。

## API 与事件

认证、设置、会话、分页历史、Agent 列表、提交 Run、读取状态、暂停、继续、取消、页面租约和 SSE 共用 `/api/v1`。没有另复制一套 `/travel/chat` 或 Python 身份 API。

- `/agents` 分别报告 general/travel 的可用性。
- `/conversations/:id/runs` 依据会话归属执行；幂等键复用同请求，内容变化冲突。
- `/runs/:id/events` 支持持久化事件重放，流式 delta 为临时输出。
- 新增 `travel.phase`，只允许规定阶段和业务 ID，不能夹带提示词、密钥或任意供应方响应。
- `travel.model_call` 记录每次真实模型调用的阶段、耗时和实际 Usage；Usage 缺失为 unknown，包含辅助模型及失败调用。
- 最终成功在提交后发布；工具已提交不等于整个 Run 一定成功。

`paused` 可手动继续；`interrupted` 不能证明有安全断点；`failed` 是执行失败；`timed_out` 是活动执行超时；`cancelled` 为主动结束或新问题替代。未知结果和过期证据有独立说明。

## 验证方式

```powershell
pwsh -NoProfile -File .\scripts\verify.ps1 -SkipNpmInstall
```

该脚本覆盖完整 Go 测试、真实 PostgreSQL、vet、后端构建、前端组件、TypeScript 与生产构建。数据库缺失时发布验证直接失败。集成测试只在独立测试库的随机 schema 执行，不翻译原 Python 的上千个测试。

关键检查集中在少量综合用例：`TestTripRules`、`TestTravelTransactions`、`TestTravelQueries`、`TestTravelMemoryIsolation`、`TestTravelWorkflowRecovery`、供应方协议和现有平台生命周期回归。真实模型、供应方和 MCP 使用显式开启的冒烟检查，不能以跳过作为接通证据。

Linux race 在 GitHub Actions 执行；本机 Windows 没有有效 race 工具链。历史 CI 只能证明对应历史提交，当前分支状态见最新验收记录。

### 最近一次真实模型测量

固定请求各运行3次，工具使用明确标注的模拟数据，数据库与模型真实执行。耗时从 Agent 构造到最终 Run 持久化，不含 HTTP/SSE/UI；非流式，不报告 TTFT。模型为本地配置的 deepseek-flash。

| 场景 | 通过 | 耗时范围 | 实际模型调用 | 总 Token 范围 |
| --- | --- | --- | --- | --- |
| 只查车次与天气 | 3/3 | 2.88–3.41秒 | 2次 | 13,663–13,692 |
| 单程出差保存并选车 | 3/3 | 9.92–10.45秒 | 5次 | 29,353–29,884 |
| 三段往返＋两晚住宿＋两场活动 | 3/3 | 13.45–28.27秒 | 3–8次 | 27,235–71,551 |
| 只把会议14:00改为16:00 | 3/3 | 2.93–3.44秒 | 2次，供应方查询0次 | 14,498–14,534 |

本轮全部调用返回 Usage。多城市调用量仍有明显波动，3次样本不能证明长期稳定性；通过表示实际可实现的安排已保存，不代表酒店价格/库存或政策合规已核实。[逐次结果和输出示例](docs/travel-source-fidelity-verification.md)、[76份历史及最终脱敏样本](docs/reports/travel-source-model-2026-10-24.json)。

建议演示：登录 → 两入口与历史 → 真实差旅保存和候选选择 → 切换时继续 → 关闭发起页面 → 重开仅展示 → 手动继续 → 暂停后新问题替代 → 展示回执、修订与来源隔离。

## 尚未完成及明确限制

| 项目 | 当前边界 |
| --- | --- |
| 主 Agent 委派 | 两入口独立，未实现 Query 路由、父子 Run 与跨 Agent 委派 |
| 网页查询 | 尚无确定供应方，返回 unavailable |
| 政策知识 | 曾以自编演示集合验证 MCP 命中；本轮性能使用固定演示检索片段＋真实模型提取。企业真实制度和在线 RAG 质量仍需后续验收 |
| 预订交易 | 不出票、不订房、不付款、不退款；酒店地点不是可订报价 |
| 个性化配置 | 用户 API Key 加密管理、用户模型配置、动态 MCP 服务管理尚未实现 |
| RAG 产品能力 | 没有知识上传/索引管理界面；复用已有 RAG，不在平台重建 RAG |
| 部署与规模 | 单实例，未实现 Redis、队列、多实例调度；Docker 未实测 |
| 恢复范围 | 支持已验证安全点；强制中断、未知结果或不兼容断点不保证继续 |
| 本次交付 | 浏览器恢复、替代任务和最终多查询通过；Linux race CI 通过；正式企业政策、网页供应方及部署缺口见验收记录 |

## 文档与后端能力展示

- 正式范围：[OpenSpec 当前变更](openspec/changes/archive/2026-10-23-refactor-travel-agent-go/)。
- 技术决定：[design.md](openspec/changes/archive/2026-10-23-refactor-travel-agent-go/design.md)。
- 原系统行为映射：[source-assessment.md](openspec/changes/archive/2026-10-23-refactor-travel-agent-go/source-assessment.md)。
- 实施计划：[2026-10-23-travel-agent-go.md](docs/superpowers/plans/2026-10-23-travel-agent-go.md)。
- 实施及验证：[实施记录](docs/travel-refactor-execution.md)、[验收记录](docs/travel-refactor-verification.md)。

可以重点展示：模块化单体与依赖边界、状态机与页面租约、幂等接纳、事务和乐观修订、attempt 围栏、决策/回执恢复、证据过期处理、来源隔离、SSE 重放及真实数据库并发测试。它们有实际代码和验证，不只是架构图。

执行架构参考 [Eino](https://github.com/cloudwego/eino) 与 [chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)，MCP 接入使用 [官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)。依赖说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
