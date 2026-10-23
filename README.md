# Go Agent Platform

Go + React Agent 平台，当前 MVP 进度 7/8：前后端使用闭环已实现；正在完成交付与最终审核。真实模型网络冒烟待本地配置，尚不宣称完整验收完成。

## 正式文档

唯一正式需求来源是 [OpenSpec MVP 变更](openspec/changes/build-agent-platform-mvp/proposal.md)：

- [行为规格](openspec/changes/build-agent-platform-mvp/specs/)
- [技术设计与架构图](openspec/changes/build-agent-platform-mvp/design.md)
- [实施任务状态](openspec/changes/build-agent-platform-mvp/tasks.md)
- [详细实施计划](docs/superpowers/plans/2026-10-22-agent-platform-mvp.md)

README 只负责导航与当前状态，不另行定义产品行为。用户已选择单代理执行；进度与证据见 [执行记录](docs/execution-ledger.md) 和 [验证记录](docs/verification.md)。

## 已确认方向

Service 与 Agent 并列，Application 协调复杂用例；业务操作经 Service，运行数据经 Store，Repository 统一实现持久化。

普通聊天参考 [Eino chatwitheino](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)，复用执行和断点恢复架构。首版启用聊天与只读工具，RAG、审批、本地执行分阶段启用。

差旅只预留独立入口；以后重构已有 Agent 并接入平台存储接口，当前不提供模拟行程。

页面内切换保持任务执行，关闭发起页请求安全暂停；重开仅恢复显示，用户手动继续。暂停时直接发新问题结束旧任务，旧历史可见，但退出后续模型上下文且旧断点失效。

## 项目结构

```text
backend/
  cmd/server/                   服务启动
  internal/
    bootstrap/                  构造、注入和注册
    config/                     进程配置
    router/                     Gin 路由
    middleware/                 认证、日志、请求控制
    handler/                    HTTP 与 SSE
    application/chat/           聊天用例协调
    service/                    auth / user / conversation / settings
    agent/
      contracts/                执行契约
      manager/                  注册、Run、页面执行归属
      agents/general/           Eino example 的平台适配
      agents/travel/            差旅接入预留
      tools/                    工具授权及 Eino 桥接
      memory/                   上下文与记忆适配
      ports/                    运行数据存储接口
    agenttools/                 具体工具到业务能力的桥接
    repository/postgres/        持久化及事务
    client/llm/                 模型客户端
    model/                      稳定实体
  migrations/                   后续数据库迁移
  tests/integration/            后续真实数据库测试
frontend/src/                   React 前端预留
openspec/                       正式变更文档
docs/superpowers/plans/          实施计划
```

尚未实现的包仅用 .gitkeep 保留位置，不代表已有功能。

## 当前环境与验证状态

- 已发现 Go 1.27.2、Node 24.20.0、OpenSpec 1.12.0。
- 已按用户要求安装并注册 PostgreSQL 17.11；服务和开发/测试库均已验证，见 [数据库说明](docs/local-postgres.md)。未安装 Docker。
- 后端现有 33 项测试、go vet 和构建通过，真实 PostgreSQL 与 HTTP 冒烟通过；前端尚未实现。
- 已有本地 Git 分支 feat/agent-platform-mvp；没有远程推送或 PR。
- 真实模型冒烟尚未运行；race 因本机工具链启动失败未通过，详见验证记录。

启动当前基础 API（读取本地 .env，不打印凭据）：

```powershell
pwsh -File scripts/dev-server.ps1
```

默认 http://127.0.0.1:8080，健康检查 /health/live、/health/ready。普通聊天目前返回 model_unavailable，差旅返回 agent_unavailable，不生成模拟回答。

运行已有测试（PowerShell，在项目根目录）：

```powershell
$env:GOMODCACHE = Join-Path (Get-Location) '.cache/go-mod'
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
Set-Location backend
go test ./... -count=1
go vet ./...
```

