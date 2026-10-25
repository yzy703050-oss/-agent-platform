# 通用聊天联网搜索 Implementation Plan

> **For agentic workers:** 使用 superpowers:executing-plans 按任务实施。用户已选择单代理，本计划不派子代理。先阅读对应 OpenSpec change，计划仅展开执行与验证，不另定产品行为。

**Goal:** 给通用聊天接入官方 DuckDuckGo v2，形成可观察、有来源、可恢复的搜索工具闭环。

**Architecture:** 保持 Handler → Application → Manager → General → Eino → 工具 → 外部客户端分层。搜索结果、事件、断点沿用 Repository；设置工具保留 Service 授权链路，差旅不参与本次修改。

**Tech Stack:** Go 1.26、Eino v0.9.0-beta.1、官方 DuckDuckGo v2 v2.0.0-20261019121619-eee37f63ff95、PostgreSQL、React/Vitest。

**Spec:** [需求](../../../openspec/changes/add-general-web-search/specs/general-web-search/spec.md)、[技术设计](../../../openspec/changes/add-general-web-search/design.md)、[任务表](../../../openspec/changes/add-general-web-search/tasks.md)。

## Global Constraints

- WEB_SEARCH_ENABLED 默认 true，false 不注册工具；不修改差旅提示词、Schema、Service 或工作流。
- query 为 trim 后 1～500 字符；time_range 为缺省/空、d、w、m、y；未知字段、类型错误和多个 JSON 值拒绝。
- 最多 5 条结果，标题 200 字符、摘要 800 字符、URL 2048 字节；完整 JSON 最大 16384 字节。
- 使用 TOOL_SECONDS 和父 context，无新自动重试；原 HTTP 响应体限制 1 MiB 并关闭。
- 只允许有效 http/https 来源；公网结果不作为指令；不抓取正文。
- 存储统一复用 Repository，无新增数据库表或缓存；指纹包含工具开关、提示和组件版本。
- 只补关键综合测试；本地 .env 内容、密钥、凭证不得进入输出、报告或 Git。

## Review Focus

- 官方返回验证页面或非预期 HTML：不能当有结果或普通空结果；任务 1 用固定响应验证。
- 取消恰好发生在官方 3 秒分页等待：无新的实际 HTTP，允许设计已披露的有限同步返回延迟；任务 1 验证。
- 返回体超限、读取中断或非 200：原响应体必须关闭；任务 1 验证，不修改共享差旅 Transport。
- 同会话包含设置、搜索和差旅样式 JSON：来源渲染必须匹配 ToolCallID 的工具名称；任务 3 验证。
- 开关切换后恢复旧任务、已完成搜索后恢复：前者拒绝不兼容断点，后者不重发已完成搜索；任务 2、3 验证。

## 实施前检查

当前分支 `codex/general-web-search` 基于 `origin/feat/agent-platform-mvp`，差旅修复 PR #1 独立保留。产品代码未修改，官方模块仅下载到忽略缓存。用户已审阅并确认实施；执行方式继续单代理。

- [x] 阅读 `openspec instructions apply --change add-general-web-search --json` 及全部 change 文档。
- [x] 运行受影响 General/agenttools/config 基线测试；记录输出到 `.cache/execution/general-web-search/`。基线失败先查明，不混入搜索修复。
- [x] 执行中每完成下述可交付任务，再据实际测试输出更新 OpenSpec tasks.md 与本计划记录。

命令约定：从仓库根目录在 PowerShell 先执行 `. ./scripts/load-env.ps1`，然后 `Set-Location backend`。前端命令在 frontend 下执行。不得在日志中打印环境变量值。

### Task 1: 官方客户端与有界工具（OpenSpec 1.1）

**Files:**
- Create: `backend/internal/client/websearch/client.go`、`transport.go`、`client_test.go`
- Create: `backend/internal/agenttools/web_search_tool.go`、`web_search_tool_test.go`
- Modify: `backend/go.mod`、`backend/go.sum`

**Interfaces:**
- client/websearch：`Request{Query string; TimeRange string}`、`Result{Title, URL, Summary string}`、`Response{Status, Message string; Results []Result}`，所有 JSON 字段使用小写 snake_case。
- `websearch.New(ctx context.Context, transport http.RoundTripper) (*Client, error)`，nil transport 克隆 http.DefaultTransport；内部调用官方 NewSearch，MaxResults=5、RegionWT，禁用重定向。
- `(*Client).Search(ctx context.Context, request Request) (Response, error)`：供应商异常统一 ErrUnavailable，取消/超时可通过 errors.Is 识别；有效结果与正常无结果分别返回 ok/no_results。
- agenttools：`WebSearcher interface { Search(context.Context, websearch.Request) (websearch.Response, error) }`。
- `NewWebSearchTool(searcher WebSearcher, timeout time.Duration) *WebSearchTool`，实现 Eino tool.InvokableTool。

- [x] **先写失败测试。** 采用少量表驱动综合测试：`TestClientOfficialParserAndBounds` 用自定义 RoundTripper 返回含有效结果的 HTML，断言实际官方解析器解析 URL/标题/摘要、5 条上限、非法 URL/重复/超长字段、16384 字节 JSON；`TestClientFailureAndCancellation` 覆盖非 200、验证 HTML、正常无结果、1 MiB 超限、Body 关闭、父取消及分页等待后无实际请求。`TestWebSearchToolContractAndFeedback` 验证无效字段不访问客户端、错误状态传给模型、事件开始/结束/失败及 duration_ms；父取消和事件失败返回 Go error。
- [x] **确认 RED。** `go test ./internal/client/websearch ./internal/agenttools -run 'Test(ClientOfficialParserAndBounds|ClientFailureAndCancellation|WebSearchToolContractAndFeedback)' -count=1`；预期因新接口/实现缺失而失败，确认不是网络或 DB 配置问题。
- [x] **最小实现。** 固定上述官方版本，保留现有 Eino 版本；仅新增搜索私有 Transport，在原 Body 关闭前完成有界读取；明确正常空页面识别与不可用判断；严格参数解码；结果作为结构化工具观察，业务失败返回 JSON + tool.failed，取消与存储事件失败返回 error。不 fork 官方 HTML 解析、不修改共享 providerhttp。
- [x] **确认 GREEN。** 重跑上述命令和 `go test ./internal/agenttools -count=1`；`go list -m github.com/cloudwego/eino` 仍为 v0.9.0-beta.1。检查 go.mod/go.sum 变化解释得通，不引入无关升级。
- [x] **完成记录。** 自审边界、HTTP 资源释放与 JSON 大小；任务表 1.1 勾选并记录 RED/GREEN 证据，提交本任务相关文件。

### Task 2: 配置与 General 搜索闭环（OpenSpec 2.1）

**Files:**
- Modify: `backend/internal/config/config.go`、`config_test.go`
- Modify: `backend/internal/bootstrap/bootstrap.go`
- Modify: `backend/internal/agent/agents/general/agent.go`、`agent_test.go`
- Create: `backend/internal/agent/agents/general/prompt.go`、`web_search_test.go`
- Modify: `.env.example`、`scripts/load-env.ps1`

**Interfaces:**
- `config.Config.WebSearchEnabled bool`，Load 读取 WEB_SEARCH_ENABLED，空值默认 true，非布尔值返回配置错误。
- `general.Option`、`general.WithWebSearch(agenttools.WebSearcher) Option`；现有 NewAgent 最后增加 `opts ...Option`，旧调用者无需改动。Agent 搜索依赖与提示固定在构造时，不能执行中改变开关。
- `NewExecution` 保留 get_my_settings，非 nil 搜索依赖才追加 web_search；Runner 和 RepositoryStore 不另建版本。

- [x] **先写失败测试。** `TestWebSearchConfiguration` 覆盖默认/false/非法；`TestGeneralWebSearchFlow` 用脚本模型检查暴露的工具与提示，首轮请求 web_search、第二轮消费实际工具观察并输出来源，验证 2 次模型调用与 1 次搜索；`TestGeneralWebSearchDisabledAndDirectReply` 验证禁用不注册/不宣传与启用时直答 0 搜索；错误反馈场景用失败客户端验证下一轮模型收到 search_unavailable；比较启用/禁用指纹不同，并保留设置工具测试。
- [x] **确认 RED。** `go test ./internal/config ./internal/agent/agents/general -run 'Test(WebSearchConfiguration|GeneralWebSearch)' -count=1`，确认失败对应新配置、Option 或工具注册缺失。
- [x] **最小实现。** bootstrap 按开关创建 client 注入 General；抽出条件提示，加入引用/失败/外部内容边界；指纹包括实际提示、开关、契约与官方版本。同步 env 示例与加载脚本白名单，不写实际 .env。异常初始化返回明确错误，不能静默声称搜索就绪。
- [x] **确认 GREEN。** 重跑本任务测试，再运行 `go test ./internal/config ./internal/agent/agents/general ./internal/agenttools -count=1`；既有工具、流式输出、取消及恢复测试继续通过。
- [x] **完成记录。** 核对 diff 中没有差旅路径改动，任务表 2.1 勾选，记录测试并提交。

### Task 3: 来源展示及真实存储/恢复（OpenSpec 3.1、3.2）

**Files:**
- Modify: `frontend/src/App.tsx`、`frontend/src/App.test.tsx`
- Create: `frontend/src/components/WebSearchResult.tsx`、`WebSearchResult.test.tsx`
- Create: `backend/tests/integration/general_web_search_test.go`

**Interfaces:**
- `WebSearchResult({content}: {content: string})` 渲染稳定搜索 JSON，数据错误时以普通文本展示，不执行 HTML。
- App 从已有 messages.tool_calls 建立 ID→name 映射，仅匹配 web_search 的 ToolCallID 时用此组件。搜索事件文案为“正在联网搜索…”、“联网搜索已返回结果”、“联网搜索失败”；保留原设置和差旅事件显示。
- PostgreSQL 测试使用现有 setup、Manager、General、RepositoryStore，与脚本模型/固定搜索器搭配；不增加生产接口或数据库字段。

- [x] **先写失败测试。** 前端一组综合断言：搜索事件、旧 attempt 过滤、正确配对的来源可点击、非法协议不生成链接、错误 JSON 安全回退及设置工具不误渲染。`TestGeneralSearchPersistenceAndRecovery` 真实 PG 保存工具调用/观察和事件，在已完成搜索后的支持边界暂停；重新加载只读历史不增加搜索次数；兼容恢复继续消费既有结果，开关导致指纹不同的恢复被拒绝。调用现有上下文隔离测试保证替代任务排除旧结果，不复制全套通用恢复测试。
- [x] **确认 RED。** `npm run test -- --run src/App.test.tsx src/components/WebSearchResult.test.tsx`；`go test ./tests/integration -run TestGeneralSearchPersistenceAndRecovery -count=1 -v`。PG 使用真实 TEST_DATABASE_URL，若跳过不得当通过。
- [x] **最小实现。** 完成显示组件与工具名映射；复用现有消息保存和恢复实现，新增测试若发现底座问题先查明并更新设计再修改，不顺带重构 Manager/Repository。
- [x] **确认 GREEN。** 重跑指定测试；验证搜索次数恢复前后没有重复，数据库中工具配对完整、事件可见、旧断点拒绝证据明确。
- [x] **完成记录。** 自审 XSS/协议、匹配范围、存储授权；任务表 3.1/3.2 勾选，提交本任务文件。

### Task 4: 真实验收、文档与规格核对（OpenSpec 4.1～4.3）

**Files:**
- Modify: `README.md`、`backend/internal/agent/agents/general/README.md`
- Create: `backend/tests/integration/general_web_search_real_test.go`
- Create: `docs/general-web-search-verification.md`
- Maintain: 对应 OpenSpec tasks、实施记录；私有原始日志放 `.cache/execution/general-web-search/`

**Interfaces:** 公网冒烟由 `RUN_WEB_SEARCH_LIVE=1` 显式启用；真实模型冒烟另由 `RUN_GENERAL_SEARCH_REAL=1` 启用。普通 CI 不访问公网/模型。复用 llm.New 及只在测试中包装的模型调用记录器，取供应商报告 usage，缺失不按字符估算成真实 token。

- [ ] **准备可核对验收。** 新增 opt-in `TestWebSearchLive`：实际官方客户端查询公开 CloudWeGo/Eino 资料，至少一条有效来源才通过；`TestGeneralSearchRealModel`：本地已配置模型执行明确搜索问题，至少 1 次实际搜索、最终回复带返回来源；另做普通直答及搜索失败反馈检查。先运行离线综合测试检验记录器不重发请求、不漏 Stream usage，不新增大量测试组合。
- [ ] **实际运行。** `go test ./... -count=1 -timeout 180s`（REQUIRE_DATABASE_TESTS=1）、`go vet ./...`、`go build ./cmd/server`；前端 `npm run test -- --run`、`npm run build`。Linux CI 使用已有 `go test -race ./... -count=1 -timeout 180s`，Windows 不声称完成 race。
- [ ] **公网与模型验收。** 分别设置上述开关后运行指定冒烟，记录真实日期、模型、延时、模型调用数、搜索调用数、供应商 tokens、来源及失败情况。网络失败停止标记公网通过，分析后报告限制；不得以 mock 或 web 工具代替应用客户端验收。无须跑 Python 模型对比。
- [ ] **文档与整体自审。** README 说明分层、开关、提示与工具反馈、来源展示、取消延迟限制及真实验收证据；核对无密钥、无无关升级或差旅改动。完成 specs 每条场景→代码/测试/实际输出的矩阵。
- [ ] **规格检查与完成。** `openspec validate add-general-web-search --strict`；当前 CLI 无 verify 命令，使用明确标注的人工一致性核对。Go/前端/PG、公网与模型、Linux race 都有通过证据后才勾选产品验收。若 CI 需先推分支，以必要的验证性 push 运行，不先声称完成。
- [ ] **归档与分支收尾。** 所有产品任务通过后运行 OpenSpec archive 并检查主规格，再按 finishing-a-development-branch 流程处理用户选择。创建 PR 时必须另建搜索 PR 并 attach_artifact，不更新差旅 PR 的产品范围。

## 技术复核记录

- OpenSpec 只新增 general-web-search 能力，既有平台规格继续适用；产品与技术要求分别以 specs/design 为准。
- 已核对现有 settings 工具、General 构造/指纹、Bootstrap、配置白名单、前端工具事件与消息字段；消息工具名需从 ToolCallID 配对获取。
- 已核对官方来源，识别响应体未关闭、无界读取和固定分页等待；Task 1 有明确适配与验证，不默认为官方等于平台要求已满足。
- 计划任务映射完整：Task 1→1.1；Task 2→2.1；Task 3→3.1/3.2；Task 4→4.1/4.2/4.3。无独立重复需求文档。
- 当前阶段只生成规划文档，不修改产品文件、不声称已实现或测试通过。待用户审阅后按已选单代理实施。
