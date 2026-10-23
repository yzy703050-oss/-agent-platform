# 本地运行与验收

本项目支持单实例 Go API，提供通用聊天和 Go 原生差旅两个独立入口。正式需求以 OpenSpec 为准；本次重构证据见 [差旅验收](travel-refactor-verification.md)。

## 直接运行（本机已安装 PostgreSQL）

前置：Go 1.26+、Node.js 24、PostgreSQL 17。当前电脑已注册 `postgresql-agent-platform-17` 服务，详情见 [本地数据库](local-postgres.md)。无需安装 Docker。

在项目根目录操作。已有 `.env` 保存数据库连接，**不要用示例覆盖现有文件**。新机器才复制 `.env.example` 为 `.env` 并填写实际连接。

仅在本地 `.env` 填写这三项，不发到聊天、不提交 Git：

```dotenv
LLM_BASE_URL=https://YOUR_PROVIDER/v1
LLM_MODEL=YOUR_MODEL_ID
LLM_API_KEY=YOUR_LOCAL_KEY
```

模型须兼容 OpenAI Chat Completions 和工具调用。Base URL 取服务商给出的完整 API 前缀，通常含 `/v1`。配置在进程启动时读取，修改后需要重启 API。无模型配置时仍可注册、管理会话和设置；聊天返回 `model_unavailable`。

两个 PowerShell 7 终端分别执行：

```powershell
pwsh -NoProfile -File .\scripts\dev-server.ps1
```

```powershell
cd frontend
npm ci
cd ..
pwsh -NoProfile -File .\scripts\dev-frontend.ps1
```

打开 http://127.0.0.1:5173 。请保持这个地址与 `PUBLIC_ORIGIN` 一致，不要混用 localhost 和 127.0.0.1。API 默认 http://127.0.0.1:8080 ，通过 Vite 的同源代理访问。普通前台启动用 Ctrl+C 停止。

`DB_DSN` 只用于运行库，`TEST_DATABASE_URL` 必须指向独立测试库。加载脚本只解析允许的 `KEY=value`，不执行 `.env` 内容，不加载管理员的 `.env.postgres`。

## 自动化验收

差旅查询：本地 `.env` 的 `TRAVEL_QUERY_MODE=live` 使用真实供应方，`demo` 使用明确标记的演示车次、酒店地点和天气，真实失败不会自动回退。两种模式缓存隔离。演示模式仍使用真实数据库、模型、权限、事务和断点。

演示政策原文为 `docs/fixtures/demo-travel-policy.txt`，只导入现有 RAG 的 `agent_platform_demo` 集合。`RAG_SETTINGS_PATH` 可指向 RAG 已有的 `config/travel_policy.yaml`，使用现有本地 BGE；平台标注虚构政策，不作为正式报销或合规依据。无需启动原 Python 差旅 Agent。冷启动约18秒，本机 `TOOL_SECONDS=30`，默认10秒；超时保持未核验。

```powershell
pwsh -NoProfile -File .\scripts\verify.ps1
```

要求真实测试库可连接；缺少 `TEST_DATABASE_URL` 直接失败。每项输出在 `.cache/reports/`，不提交日志。该脚本执行完整 Go 测试、vet、构建、npm ci、组件测试、TypeScript 与生产构建。Windows 上先停止 Vite，释放原生模块文件锁；也可用 `-SkipNpmInstall` 复用已安装依赖。

真实模型单独验收，先启动 API：

```powershell
pwsh -NoProfile -File .\scripts\model-smoke.ps1
pwsh -NoProfile -File .\scripts\model-smoke.ps1 -ExerciseRecovery
```

脚本创建合成测试账户和会话，验证真实模型、只读设置工具以及可选暂停/继续；正常模型调用会消耗服务商额度。只报告状态与工具数量，不打印 Key、Cookie、页面令牌或模型全文。恢复检查需要任务在暂停前尚未结束；若模型过快完成或未保存有效断点，该项明确失败，不冒充通过。未配置模型时脚本会拒绝执行。

Windows 的 race 工具链当前不可用，本机没有 race 通过证据。GitHub Actions 的 Linux `go test -race` 已实际通过，见 [CI 运行记录](https://github.com/yzy703050-oss/-agent-platform/actions/runs/39921638050)。Compose 和镜像尚未在本机实际构建。

## 手动演示路径

1. 注册、登录，设置回复偏好。
2. 点击“通用聊天”，发送“请调用工具读取我的主题和回复偏好”。应显示真实回答与工具状态。
3. 运行较长任务时，点击左侧其他会话或另一个助手入口。原任务保持执行。
4. 关闭发起页面。服务端通过离开通知或 20 秒租约超时请求暂停，安全停止最多等待 15 秒。
5. 重新打开工作台。仅恢复显示原会话；没有有效断点时显示中断，有有效断点时显示暂停。
6. 点击“继续任务”才恢复原 Run；attempt 增加，归属转移到新页面。
7. 再次暂停后直接发送新问题。旧任务结束、旧断点失效、历史仍可查看；新任务上下文排除被替代任务。
8. 差旅入口应明确未接入，不能产生模拟行程。删除有非终态任务的会话应被拒绝，先结束任务后再删除。

暂停不是逐 Token 无损续写。框架保存可恢复的执行边界；没有有效断点不能继续。恢复时的输出方式由 Eino 断点状态决定，完成结果始终通过状态事件与 GET 取得。

已验证流式模型在工具边界的安全暂停和恢复。若等待超时导致流被强制截断，该版本框架可能重放先前工具；平台会拒绝保存这种不安全断点，并显示 interrupted，不能将它当作安全暂停。普通回答自然完成时也未必产生可继续的执行断点。

## 默认预算与诊断

| 配置 | 默认 |
| --- | --- |
| MAX_ITERATIONS | 8 次模型迭代 |
| RUN_SECONDS | 每次活动执行 120 秒 |
| TOOL_SECONDS | 工具 10 秒 |
| HEARTBEAT_SECONDS / LEASE_SECONDS | 5 / 20 秒 |
| PAUSE_SECONDS | 安全停止 15 秒 |
| 并发 | 每会话 1 个非终态任务、每用户 3 个执行、进程 8 个 |
| 上下文 / 输出 | 完整 Run 分组裁剪至约 32KB；聚合输出 256KB |
| SSE | 每订阅 64 条缓冲、每 Run 8 个订阅、进程 128 个 |

`/health/live` 检查进程，`/health/ready` 检查数据库；登录后的 `/api/v1/metrics` 返回进程内基础计数。日志关联 request_id、run_id、attempt、工具耗时，不记录原始模型错误、模型 Key 或密码。数据库出错时不会切换为内存数据。

`paused` 才能继续；`interrupted` 表示无法证明存在安全断点；`timed_out` 是活动执行超时；`cancelled` 为用户结束或被新问题替代。终态写入失败不会发布成功。异常退出后的启动对账不会自动调用模型。

## 可选 Compose 路径

本机未安装 Docker，本路径只提供配置，未验证实际运行。另有 Docker 的机器可在本地 `.env` 配置不同的、URL 安全的随机 `POSTGRES_PASSWORD`（初始化管理员）与 `APP_DATABASE_PASSWORD`（受限应用角色）后执行：

```powershell
docker compose up --build
```

打开 http://127.0.0.1:8088 。数据库只在编排网络内访问，与本机 PostgreSQL 独立。现有数据卷不会因重建镜像被删除；不要用 `down -v` 处理普通重启。该编排用于本地演示，公开部署还需要自己的域名、HTTPS 与部署配置。

## 故障排查

- 启动提示 another platform instance：先确认旧 API 是否仍在运行；不要绕过数据库单实例锁。
- 模型不可用：检查本地三个 LLM 配置项并重启后端；不要将 Key 填入前端。
- 401：登录已过期或已登出。503 auth_unavailable：认证存储暂时不可用，检查数据库服务。
- 页面过期：工作台会重新申请页面身份；已有暂停任务仍须手动继续。
- checkpoint_incompatible：模型、工具或框架配置不兼容；查看历史并开始新任务，不能强行使用旧断点。

## 交付状态

基础平台 MVP 已归档，历史证据见 [真实模型验收](real-model-acceptance.md)、[验证记录](verification.md)。当前 Go 差旅重构尚未归档，最新验收及缺口见 [差旅验收](travel-refactor-verification.md)。Docker 可选路径继续明确未验证。
