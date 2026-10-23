# 数据库只读检查记录

检查日期：2026-10-23（Asia/Shanghai）。这是环境和现有 schema 的检查证据，不是差旅实现的验收报告，也不是另一份需求规格。

## 检查方式

- 使用已安装 PostgreSQL 17 的 pg_isready 和 psql。
- 只读取本地 .env 中 DB_DSN / TEST_DATABASE_URL 建立连接，密码通过进程环境传递，不打印、不写入本文、不读取模型 Key。
- SQL 放在 BEGIN READ ONLY / COMMIT 中执行。
- 没有运行迁移、创建 schema、插入或删除业务数据，也没有重启数据库或更改配置。

## 实际结果

| 检查 | 结果 |
| --- | --- |
| Windows 服务 postgresql-agent-platform-17 | Running |
| 127.0.0.1:5432 | accepting connections |
| 开发库 agent_platform | 连接成功，PostgreSQL 17.11，transaction_read_only=on |
| 测试库 agent_platform_test | 连接成功，PostgreSQL 17.11，transaction_read_only=on |
| 两个应用账号 | rolsuper=false、rolcreatedb=false、rolcreaterole=false |
| 开发库 schema_migrations | 001、002、003 |
| 开发库 public 表 | users、sessions、user_settings、page_leases、conversations、agent_runs、messages、run_events、checkpoints、schema_migrations |
| 请求幂等索引 | agent_runs 上 (user_id, conversation_id, idempotency_key) UNIQUE 存在 |
| 单会话非终态互斥索引 | one_nonterminal_run 存在，覆盖 accepted/running/pausing/paused/resuming/cancelling |
| 消息来源去重索引 | message_source_key 存在 |
| 测试库 public schema | 无业务表；符合测试在随机 schema 中创建和清理数据的现有实现 |

## 可以得出的结论

现有 PostgreSQL 正常提供连接，平台基础迁移及上述关键索引已存在。可以在此基础上继续设计增量差旅迁移，无需重新安装数据库或安装 Docker 才能推进。

## 本次没有证明的事项

- 差旅表尚未创建，其事务、并发、幂等和恢复尚未实现或验证。
- 未检查全量数据语义、备份恢复、磁盘容量、生产负载或外部数据库部署。
- 未在此次文档任务中重跑 Go 或前端测试；此前 MVP 的验收记录仍以原报告为准。
- 没有发现需要为本次设计预先清库的理由。未来新迁移必须先在隔离测试 schema 进行验证。

现有实现参考：backend/internal/repository/postgres/db.go、backend/migrations/001–003、backend/tests/integration/repository_test.go。
