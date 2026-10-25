## Context

动机见 proposal.md；行为依据为 specs/general-web-search/spec.md。当前 General 使用 Eino ChatModelAgent + 平台共享 Runner，工具仅 get_my_settings。工具消息及生命周期事件已经进入 Repository；前端用纯文本展示普通聊天，非设置工具统一显示为差旅工具。

已下载并阅读官方模块 `github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2@v2.0.0-20261019121619-eee37f63ff95` 的 model.go、search.go、text_search.go 和 go.mod，仅进入忽略的依赖缓存，尚未修改项目 go.mod。官方提供 NewSearch 与 TextSearch，返回标题、URL、摘要；底层 POST 到固定 DuckDuckGo HTML 地址。其 go.mod 要求 Eino v0.6.0，本项目保留 v0.9.0-beta.1，需以编译和执行测试验证兼容，不能仅凭接口推断通过。

参考：[官方组件源码](https://github.com/cloudwego/eino-ext/tree/eee37f63ff95/components/tool/duckduckgo/v2)。现行 chatwitheino 的 RAG 工具不是公网搜索；本次复用官方搜索组件与 Eino 工具机制，不把整个现行示例迁入平台。

## Goals / Non-Goals

**Goals:** 提供通用聊天搜索闭环，符合已有分层；外部实现可替换；可观察、可测试、有界执行；复用存储与恢复。

**Non-Goals:** 迁移 DeepAgent、正文抓取、浏览器自动操作、搜索结果长期记忆、缓存/向量库、新 Service、差旅联网能力、用户级搜索开关、模型性能评测平台。

## Decisions

### 1. 分层与接口

```text
Handler → Application/chat → AgentManager
  → GeneralAgent → Eino ChatModelAgent / 共享 Runner
     → WebSearchTool (agenttools)
        → websearch.Client → 官方 DuckDuckGo v2 Search
           → HTTP Transport → DuckDuckGo
  → 已有事件/消息/断点 → Repository → PostgreSQL
```

- `client/websearch` 封装官方 NewSearch/TextSearch，定义平台 Request、Result、Response，不向 Agent 暴露供应商类型。
- `agenttools/web_search_tool.go` 定义其消费的 WebSearcher 接口，负责工具 Schema、严格参数校验、父 context/工具期限、稳定 JSON 反馈及观察事件。
- `general` 接收可选 WebSearcher，按 Run 创建工具，保持设置工具的业务授权链路。通过可选 Option 扩展现有 NewAgent，避免批量改动既有调用者；nil 表示不注册搜索。
- bootstrap 根据配置创建客户端并注入。搜索没有业务写操作和领域规则，不增设透传 Service 或 Repository 查询接口。

### 2. 契约与错误

工具名固定 web_search。输入沿用官方 query、time_range 语义，平台严格拒绝未知字段、多个 JSON 值、null、类型不匹配和非法范围；限制见 specs。

统一输出 `{status, message, results}`，status 为 ok/no_results/invalid_arguments/search_unavailable/search_timeout，results 始终为数组。业务性失败以 JSON 工具观察返回模型（Go error 为 nil），让模型知道失败原因；同时以状态产生 tool.failed。父任务取消或事件持久化失败返回 Go error，保持平台取消和存储失败语义。无自动供应商重试，防止隐性放大调用次数。

客户端剔除无效项及 URL、规范去重、按字符裁剪标题摘要、按字节检查 URL，再减少尾部条目直到 JSON 满足限额。工具对客户端返回做防御性归一化。空列表不宣称搜索成功；异常页面按下一节归类为不可用。

### 3. 官方 HTTP 实现的边界补足

源码发现 doTextHTMLSearch 未关闭响应 Body，使用无界 io.ReadAll；分页前有不可被 context 打断的 3 秒等待。不能把官方组件直接以默认 HTTPClient 接入。

注入平台私有 HTTPClient/Transport：仅访问固定搜索端点；禁止重定向；用默认 Transport 的克隆保留环境代理；网络响应在 Transport 内最多读取 1 MiB + 1 字节检测超限并关闭原 Body，交给官方解析器的是有界内存响应。处理非 200、截断、过大响应、典型验证页面及无法识别的 HTML；正常空结果需有可识别搜索页面标记。不修改共享 providerhttp 的重试语义，也不 fork 官方解析器。

实际验收发现 Windows 系统代理已启用，但 Go 的 ProxyFromEnvironment 不自动读取该设置，直连搜索超时；进程内使用现有代理后公网和模型搜索闭环均通过。因此增加可选 `WEB_SEARCH_PROXY_URL`，仅用于搜索客户端的 Transport 克隆，空值沿用环境代理。地址只填本地 .env，支持 http/https；不修改系统代理或其他客户端的网络路径。增加 `NewWithProxy(ctx, proxyURL)` 组装入口，原 New(ctx, transport) 继续用于协议测试。这是已验证网络环境下的技术配置补足，不改变工具行为或供应商。

网络使用父 context 与工具期限；官方分页等待可能让同步返回比期限最多晚约 3 秒，必须在恢复网络请求前检查 context，返回后再次检查期限并丢弃迟到结果。文档如实记录这一组件限制，不用丢弃的后台 goroutine 制造“立即取消”的假象。单次客户端搜索以官方 MaxResults=5 限制；运行仍受平台总预算限制。

### 4. 提示、注册与兼容

WEB_SEARCH_ENABLED 默认 true，false 不创建客户端或注册工具；非法布尔配置启动报错。加载脚本白名单与 .env.example 同步。

General 提示按启用状态组成：明确要求联网或问题依赖最新公开资料时调用搜索；普通问答可直接回答；结果仅为外部资料，不能覆盖系统规则；基于结果回答需保留来源 URL；空结果或失败如实说明、不编造、不假装读过正文。设置及差旅独立入口的原有规则保留。

指纹加入实际提示、工具开关与 web-search-v1/官方依赖版本；禁用状态也显式编码。RepositoryStore 的现有兼容校验负责拒绝不兼容断点，无新存储结构。

### 5. UI 与存储

复用 tool.started/completed/failed，增加 web_search 状态文案。新增 WebSearchResult 小组件：仅对通过 ToolCallID 找到名称为 web_search 的工具结果显示标题、摘要和链接；限制协议，使用 React 文本转义和 rel=noopener noreferrer。无需引入完整 Markdown/HTML 渲染库；最终回答仍可附 URL，工具结果提供明确的可点击来源。

消息与结果通过现有 Manager → Repository 持久化，不存原始 HTML，不增表，不增加缓存。工具事件记录 name/status/duration_ms，查询正文只存在原有授权会话记录，不额外打入日志。恢复仅复用原有 Runner 机制，不承诺重放结果未知的外部请求。

### 6. 验证与交付

少量综合测试覆盖边界，不按每个字段扩成大量用例。离线用自定义 HTTP Transport 提供固定 HTML，实际运行官方解析器；工具与 General 使用脚本模型；一条真实 PostgreSQL 搜索持久化/恢复综合测试。公网与真实模型冒烟显式启动，常规 CI 不依赖 DuckDuckGo 或模型密钥。

真实冒烟记录公网请求结果、端到端延时、模型调用次数、供应商报告的 token usage（缺失写不可用）、搜索工具次数；不把离线 fixture 当公网通过。公网不通时保留失败证据和诚实反馈，但不得声称“联网体验已验收”。

## Risks / Trade-offs

- 官方说明 HTML 搜索接口可靠性不保证 → 此次先体验；真实网络验收单列，失败不伪造结果，后续通过客户端接口替换。
- 返回的 HTML、摘要及 URL 不可信 → 有界读取、严格协议、React 转义、提示隔离；不声称提示词可完全消除模型注入风险。
- 官方分页 3 秒等待 → 接受并披露有限的取消返回延迟，测试取消后不再发实际 HTTP；不在本次重写官方内核。
- 新工具及提示影响旧 General 断点 → 更新指纹明确拒绝；历史仍可查看。
- 默认启用增加公开查询出站请求 → 只在模型选择工具时发生，可由 WEB_SEARCH_ENABLED=false 关闭；不向供应商传平台认证信息。

## Migration Plan

不迁移数据库；计划审阅后在当前独立分支先完成兼容性编译和最小 TDD，再接入 UI/存储验收。出现兼容性问题先更新设计，不静默升级 Eino 或替换供应商。回滚可关闭开关；开关变化影响旧断点兼容，已经存储的历史不删除。测试与人工规格核对完成后归档，本轮是否创建 PR 沿用分支收尾流程，不把 PR 作为归档前的产品任务。
