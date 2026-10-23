# SDD ledger — plan: docs/superpowers/plans/2026-10-22-agent-platform-mvp.md

2026-10-23：用户选择单代理执行，已确认按文档推进；当前代理实施并整体自审，不派子代理。

基线提交：dde48a5；分支 feat/agent-platform-mvp。

Ruling: 新初始化仓库没有其他工作，直接在本任务专用分支实施，不另建 worktree；保持用户指定目录可直接查看。

Pre-flight: Task 1→4/5 共用 Execution 与 Checkpoint 契约；先以固定版本实际框架测试确定协议，失败时协调 design。
Pre-flight: Task 2→3/4/5 共用身份、消息与 Run 存储；正式数据仅 PostgreSQL，不以内存替代。
Pre-flight: Task 5→6/7 页面租约独立 SSE 订阅，继续任务采用原 Run、递增 attempt。

Task 1: complete；真实 Eino v0.9.0-beta.1 + 磁盘测试 Store，6 项测试通过；go test ./... -count=5 -timeout 60s 通过，go vet ./... 通过。
Task 1 RED: 首次运行因缺少 NewRunner/RunnerConfig 实现而编译失败；依赖解析已完成，不以网络错误代替红灯。
Task 1 additional RED: 断点在构造后被删除导致框架等待新输入，测试 20 秒超时；修复为恢复 Store 缺失时明确返回 ErrCheckpointMissing，复测通过。
Task 1 Ruling: 恢复探针采用不依赖网络凭据的可控模型与只读工具，执行循环、暂停和恢复全部使用真实 Eino；测试 Store 使用临时磁盘文件，证明不依赖原执行对象的内存。生产 Store 将由 Task 2 提供。
Task 1 Ruling: 除固定版本和配置指纹校验外，增加恢复期断点必须存在的约束，避免竞态静默启动新任务。
Task 1 race: 本机 GCC 在沙箱启动失败，沙箱外 race 测试程序也以 0xc0000139 退出；未取得 race 通过证据，需支持工具链或 CI 验证。
Task 2: complete；真实 PostgreSQL 17.11 上 11 项事务测试和 3 项配置测试通过，全套现有 20 项测试及 go vet 通过。
Task 2 RED: Repository/Migrate/存储输入类型未实现而编译失败；配置测试因 Load 未实现而失败。
Task 2 Ruling: 用户要求“安装并注册”，安装本机 PostgreSQL Windows 服务，仅本机监听，分离开发/测试角色，凭据不提交。
Task 2 Ruling: pgx 固定 v5.11.0，模块最低 Go 版本提高至 1.25.0，本机 1.27.2 满足要求。
Task 2 Ruling: CheckpointStore 使用 GetCheckpoint/SaveCheckpoint/InvalidateCheckpoint，避免其他仓储 Get/Save 重名；Eino 桥接仍使用 Get/Set/Delete。
Task 3: complete；全部现有 33 项测试、vet 和构建通过；实际启动 API，健康/就绪检查正常，未登录 /me 返回 401。
Task 3 RED: auth/conversation/settings 构造器、router.New 未实现；资料仅返回 ID、生产 Cookie 不安全、第 11 次登录未限流均有失败测试，修复后通过。
Task 3 Ruling: Gin v1.12.0、x/crypto v0.58.0 将模块最低 Go 提高至 1.26.0；本机 1.27.2 满足。
Task 3 Ruling: 注册/登录共用每 IP 每分钟 10 次限流，内存有界，关闭默认代理信任。
Task 4–8: not started；接下来聊天接线与恢复 API，真实模型尚未验证。
