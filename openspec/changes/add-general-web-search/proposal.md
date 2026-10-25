## Why

通用聊天 Agent 当前只有读取用户设置的工具，不能获取公开网络信息。用户要求仿照 Eino 工具机制增加联网搜索，并已选择 Eino 官方 DuckDuckGo v2 先接通体验。

## What Changes

- 通用聊天增加按需调用的 `web_search` 工具，返回有大小限制的标题、摘要、来源 URL，支持有限时间范围。
- 使用官方 DuckDuckGo v2 搜索组件，平台负责输入校验、结果整理、超时、失败反馈与工具执行事件。
- 模型根据问题选择是否搜索；使用搜索资料作答时展示来源，搜索失败时如实说明，不伪造联网结论。
- 前端显示搜索状态和可点击的来源；复用既有消息、事件、断点持久化机制。
- 提供服务端开关；保持差旅 Agent 的工具、提示词与执行链路不变。

## Capabilities

### New Capabilities

- `general-web-search`：通用聊天按需联网搜索、有界工具执行、来源展示和恢复兼容性。

### Modified Capabilities

无。现有设置工具、任务互斥、权限、暂停恢复与上下文隔离要求继续适用。

## Impact

涉及 `client/websearch`、`agenttools`、`agent/agents/general`、配置与 bootstrap、前端消息展示及少量综合测试。新增官方 DuckDuckGo v2 模块，保留 Eino v0.9.0-beta.1；无数据库迁移、Redis、搜索缓存、网页正文抓取或新业务 Service。

独立分支 `codex/general-web-search` 基于远端默认分支 `feat/agent-platform-mvp`，与差旅修复 PR #1 分开交付。
