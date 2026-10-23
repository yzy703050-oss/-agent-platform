# 本地 PostgreSQL

2026-10-23 按用户要求安装并注册 PostgreSQL 17.11。

- 目录：C:\Program Files\PostgreSQL\17。
- 服务：postgresql-agent-platform-17，NetworkService 身份，开机自动启动。
- 仅监听 localhost:5432；IPv4 与 IPv6 回环地址均已核验。
- 开发库 agent_platform、测试库 agent_platform_test；独立低权限账户，无超级用户和创建角色权限。
- .env 保存 DB_DSN 与 TEST_DATABASE_URL；.env.postgres 保存管理凭据。均被 Git 忽略，ACL 限制当前用户、Administrators 与 SYSTEM，不要分享或提交。
- 已删除含密码的临时安装选项文件；未安装 pgAdmin、Stack Builder 或 Docker。

## 测试命令

在项目根目录 PowerShell 执行，不打印密码：

```powershell
$env:GOMODCACHE = Join-Path (Get-Location) '.cache/go-mod'
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
$line = Get-Content -LiteralPath '.env' | Where-Object { $_.StartsWith('TEST_DATABASE_URL=') }
$env:TEST_DATABASE_URL = $line.Substring('TEST_DATABASE_URL='.Length)
Set-Location backend
go test ./... -v -count=1
go vet ./...
```

集成测试只在测试库创建随机 schema，并在结束时清理。缺 TEST_DATABASE_URL 时显式跳过，不能作为发布验收通过证据。

## 服务管理

管理员 PowerShell：

```powershell
Get-Service postgresql-agent-platform-17
Restart-Service postgresql-agent-platform-17
& 'C:\Program Files\PostgreSQL\17\bin\pg_isready.exe' -h 127.0.0.1 -p 5432
```

数据库安装和基础存储验证完成不代表完整 MVP 已完成。

