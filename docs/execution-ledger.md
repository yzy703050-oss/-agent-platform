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
Task 4: complete；新增 9 项测试通过，全套 42 项（含真实 PostgreSQL）通过，go vet 通过。
Task 4 RED: manager/memory、NewSettingsTool、NewAgent 未实现，目标测试编译失败；实现后并发幂等、配额、完成事务失败、上下文排除和迟到写入测试通过。
Task 4 Ruling: 首版唯一工具通过 Eino ToolsNode 注册并执行；身份固定在每次执行创建的工具实例，不增加第二套工具调度器，未来多工具授权时再扩展 registry。
Task 4 Ruling: 上下文按完整 Run 裁剪，32KB UTF-8 预算；当前任务本身超预算直接失败，不截断工具配对或静默丢弃本次问题。
Task 4 model smoke: .env 中三个 LLM 配置项尚未齐备；用户将自行填写，未请求或输出秘密；网络冒烟留在 8.1，不能据单元模型夹具宣称实际模型已验证。
Task 5: complete；全套 60 项测试（真实 PostgreSQL）与 vet 通过。SSE 断开测试依赖 Task 6 的端点，在 6.1 一起交付。
Task 5 RED: LeaseManager/Pause/Resume/Reconcile 未实现编译失败；断点确认失败停在 pausing、工具结果重复、撤销 Session 后仍能读取设置三项断言失败，修复并通过。
Task 5 evidence: 实际 Eino TurnLoop + RepositoryStore 保存 PostgreSQL 断点，销毁内核对象后创建新实例恢复，已完成工具不重放；不兼容指纹时模型调用次数为零。HTTP 测试覆盖页面令牌拒绝、暂停、GET 不自动恢复、手动继续与结束。
Task 5 Ruling: 新增 002 迁移记录消息 attempt 和稳定 source_key；以 Run + 工具调用标识去重，而执行尝试作为存储围栏，避免跨恢复重复展示。
Task 5 Ruling: 暂停/结束在状态提交后使用独立有界上下文完成停止与落库，HTTP 客户端断开不会撤销已接受的生命周期操作。正常关闭服务先请求安全暂停，再释放数据库单实例锁。
Task 6: complete；全套 71 项后端测试（真实 PostgreSQL）与 go vet 通过。
Task 6 RED: EventHub/事件查询/部分输出字段未实现；超时误记 failed、最终回答替换 partial 后重复使用旧 ordinal 两项断言失败，修复为 timed_out 和先分配新 ordinal 再删除 partial，复测通过。
Task 6 evidence: 真实 Eino Stream 分块、真实 Eino + PostgreSQL 两次工具调用与最终输出持久化、完成通知先提交、慢订阅有界、seq 去重、错误脱敏、跨用户 SSE 拒绝、SSE 断开不暂停、部分输出暂停后仍可查询。
Task 6 Ruling: EventHub 仅推送临时 delta 或已提交变更通知；SSE 先订阅后读 PostgreSQL 持久事件，依靠 seq 去重，并每 2 秒核对持久事件与 Session，避免遗漏通知。缓冲每订阅 64 条、每 Run 8 个、进程 128 个。
Task 6 Ruling: 聚合文本上限 256KB，每 250ms 或 4KB 保存并在停止时刷新；partial 以历史消息保存，完成事务替换为最终消息。暂停 finalize 等待执行回调落库结束。
Task 6 Ruling: 该版本 TurnLoop 恢复分支 Runner 默认非流式，沿用框架恢复语义；初次执行提供真实流式增量，恢复成功后通过终态事件与 GET 取得完整输出，不伪造 Token 增量。
Task 7: complete；10 项组件测试、TypeScript 检查和生产构建通过；浏览器连接真实 API 验证登录、独立入口创建与历史回到原 Agent。
Task 7 RED: App 缺失导致测试失败；可访问名称不完整、已有 partial 隐藏后续增量、重开不显示上次会话、关闭设置应用未保存主题均由失败测试确认，修复后通过。
Task 7 Ruling: 浏览器仅保存 CSRF token 与按用户区分的上次会话 ID；Session 保持 HttpOnly Cookie，页面身份每个文档重新注册，不保存模型 Key、消息或断点。重开上次会话仅 GET 状态，仍须手动继续。
Task 7 Ruling: 本地浏览器使用单独的合成验收账户与测试会话；未配置真实模型时明确显示不可用，没有注入演示回答。模型相关浏览器端到端验收在 8.1 单列。
Task 8: in progress；交付脚本、最终自审与规格证据映射。

Task 8 implementation/verification: 本机交付与代码审核完成；scripts/verify.ps1 通过，84 项后端测试无跳过、13 项前端测试、go vet、Go 构建、TypeScript 与前端构建、npm ci 全部通过。Windows 原生文件锁导致首次 npm ci 失败，停止本任务 Vite 后干净安装通过，已记录操作说明。
Task 8 RED→GREEN: 认证/CSRF 存储故障、Origin 尾斜杠、run_conflict 错误代码、工具调用组 ID 碰撞、工具时限、跨用户恢复、前端账号切换、旧 attempt 增量、心跳间隔配置均有失败证据并修复。
Task 8 recovery audit: 完整验收发现非阻塞 Stop 的竞态，专门 100 轮未复现，源代码证实异步提交；改为等待 TurnContext.Stopped，再等待退出与保存。相关原有 6 项探针重复 100 轮通过；最终包含流式测试的 8 项恢复探针重复 50 轮通过。
Task 8 Ruling: 流式超时强制停止曾产生已完成工具重放；此类流取消完整消费框架事件，但禁止保存 checkpoint，明确 interrupted。安全流式工具边界的新实例恢复通过，工具调用 ID 证据确认已完成工具不重放。这落实 design D6 的“不能证明安全则中断”。
Task 8 Ruling: 纠正 Task 6 关于恢复一定非流式的推断。实测 Eino 恢复可能继承保存状态调用 Stream；平台消费实际事件并以持久化终态输出为准，不承诺逐 Token 续传。
Task 8 Ruling: 详细计划中的部分模块文件按实际职责合并，不增加空的运行管理/工具调度实现。唯一正式行为规格未改变；路径更新至 design D2。
Task 8 final review: 作者整体自审（用户选定单代理方式，未派独立评审）；发现及修复见 docs/code-review.md。App 协调/渲染文件偏长作为小项留待后续 UI 变更。
Task 8 spec audit: docs/spec-verification.md 对应全部 36 个 Scenario；openspec validate build-agent-platform-mvp --strict 通过。CLI 无 verify 子命令，项目无额外 verify 工作流，采用人工逐项核对，不冒称运行 verify。
Task 8 pending: .env 三个 LLM 配置项仍为空；model-smoke.ps1 按预期拒绝运行，未调用真实模型。真实模型与完整浏览器恢复验收待配置；Linux race 和 Docker/镜像尚无运行证据。8.1 保持未完成，不归档、不合并、不推送。

Task 8 real model: 2026-10-23 本地三个 LLM 配置齐全。查明旧 PID 968 占用 8080、记录 PID 已失效；核实旧进程路径后替换为最终构建。初次 readiness 不能证明新进程成功，改为以实际模型工具响应验证。model-smoke.ps1 及 -ExerciseRecovery 实际通过，未打印配置值。
Task 8 acceptance Ruling: 原三千字长流中途暂停触发不安全流取消，按设计返回 interrupted；将成功恢复探针改为短工具步骤启动后暂停，实测通过，不修改产品行为。长流失败保留在 real-model-acceptance.md，不能声称任意 Token 恢复。代价：需要在支持的边界暂停，普通长回答不保证可继续。
Task 8 browser: 真实回答/工具结果、页面内切换仍执行、pagehide leave→paused、重开不执行、显式恢复 attempt=2 完成、新问替代与旧历史保留均通过。实际关闭页面未及时通知时可能先完成或中断，未虚报恢复成功；合成任务数据库核对旧 Run cancelled/context_eligible=false/checkpoint.valid=false。
Task 8 final regression: pwsh -NoProfile -File scripts/verify.ps1 -SkipNpmInstall 于 15:34 通过；真实 PostgreSQL 84 PASS/0 FAIL/0 SKIP，前端 13 PASS，vet 与后端/前端构建通过。启动最终 API 恢复本地服务。
Task 8 public repository: 用户授权新建 GitHub 仓库并明确公开。发布前扫描全部历史 247 个 blob，未发现已配置模型密钥、数据库 DSN/密码或私钥；.env、.env.postgres、.cache 报告被忽略。GitHub 连接器无建仓能力；网页已登录，但后续操作因自动审批网络故障没有执行，已请用户手动建空仓并提供链接，随后继续 Git 推送与 Linux CI。
Task 8 pending (supersedes prior model-config pending): 真实模型验收已完成，8.1.4 已勾选。design D11/实施计划要求的 Linux race CI 尚无结果；本机 WSL 未安装、Windows race 既有工具链错误，不安装更多系统组件。8.1.5 与归档保持未完成；Docker 仅为可选路径并明确未验证。
Task 8 repository update: 用户已手动创建公开仓库 https://github.com/yzy703050-oss/-agent-platform（名称保留开头连字符）。GitHub 只读连接器确认 public、空仓、default_branch=main。后续 git add/commit 因自动审批服务连接断开没有执行，重试单独 git add 仍未执行；不是权限或代码安全拒绝。当前没有 origin、没有新验收提交、没有推送或 CI 运行，HEAD 仍为 840f456。所有真实模型验收文档和截图保留在工作区，等待审批服务恢复后继续。
