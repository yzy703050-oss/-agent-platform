# MVP 整体代码自审

方式：用户选择单代理实施与整体自审。由作者按 Superpowers 代码评审清单核对，未派子代理；不是独立评审。

范围：文档基线 `dde48a5` 之后的后端、迁移、React、启动脚本与部署文件；对照 OpenSpec specs/design/tasks、详细实施计划和执行记录。

## 已修复的主要发现

| 发现与影响 | 失败证据 | 最终处理 |
| --- | --- | --- |
| 认证/CSRF 存储故障误当作身份失效或伪造 | TestAuthStorageFailureRedacted、TestCSRFStorageFailureRedacted | 返回 503 auth_unavailable，不返回底层错误 |
| 同会话并发没有按规格返回 run_conflict | TestHTTPPageLeaseAndManualRecovery | 统一错误代码；仍兼容内部 ErrConflict 判断 |
| 恢复先查本地运行表，可能向其他用户透露任务存在 | TestCrossUserLifecycleDenied | 先验证资源归属，跨用户统一拒绝 |
| 工具调用组拼接 ID 可能碰撞，误去重丢消息 | TestDistinctToolCallGroupsDoNotCollide | 对 JSON 编码的 ID 数组取稳定键 |
| 工具在时限后返回 nil 错误，仍会被记成功 | TestToolDeadlineDoesNotReportSuccess | 返回前再次检查工具上下文，超时记为失败 |
| 同一浏览器切换账号残留上一账号工作区 | switchingAccountClearsPreviousWorkspace | 状态绑定用户，在新身份展示前清空旧工作区 |
| 前端可能接受旧 attempt 的迟到文字 | ignoresDeltasFromPreviousAttempt | 同时检查 Run 和 attempt |
| 页面心跳配置未传到前端 | TestPageLeaseReturnsConfiguredHeartbeat、usesServerHeartbeatInterval | 租约返回 heartbeat_ms，前端按服务器配置调度 |
| 尾部斜杠使有效站点 Origin 被拒绝 | TestOriginTrailingSlashNormalized | 配置阶段规范化 |
| 非阻塞 Stop 被误当作取消已生效，探针偶发跨过目标安全边界 | TestCheckpointResumeAcrossExecutionInstances 在完整验收中失败；重复专门测试不稳定复现，源代码明确 Stop 非阻塞 | 等待 TurnContext.Stopped，随后确认退出与保存；专门测试重复验证 |
| 强制截断流后的框架快照可能重放已完成工具 | 流式探针曾出现工具累计 2 次而预期 1 次 | 此类停止拒绝 checkpoint 保存；安全流式工具边界恢复另测，不将不安全快照标为可恢复 |

前面阶段已修复的断点缺失、虚报暂停、部分输出遮蔽、历史游标复用、完成事务回滚等问题，详见执行记录及对应测试。

## 边界与失败路径核对

- Service 与 Agent 并列；Handler 没有直接调用模型或 SQL。Application 通过 Service 验证会话，通过 Manager 执行。业务工具经 SettingsService；运行数据经 Store/Repository。
- 真正执行与恢复使用 Eino，产品不含假回答路径。标准消息保留工具调用与结果；断点为 opaque bytes，不向前端返回。
- Run 准入、最终输出、暂停确认和新问替代由事务处理；执行版本和状态限制迟到写入。并发、竞争、跨用户与失败回滚均有真实数据库测试。
- 页面令牌与登录 Session 绑定；SSE 与页面租约独立；重开仅 GET，继续必须 POST；启动对账没有自动执行。
- 密码/Session/CSRF/page_token 保存摘要；API Key 仅留在进程配置。日志不输出原始模型错误、认证正文或凭据。脚本不执行 `.env` 内容。
- EventHub、模型迭代、输出与上下文、请求正文、工具时限、活动时限、执行数均有界。普通启动没有 SQLite/内存数据回退。
- Compose 给应用使用受限数据库角色，管理员与应用密码分开；只绑定本机前端端口。本机未验证 Docker 路径。

## 暂缓的小项

- App.tsx 协调与工作区渲染仍偏长；可以在后续 UI 变更时提取 ChatWorkspace，当前不增加额外抽象。
- Linux race 与 Docker 运行验收尚未执行；真实模型与浏览器任务恢复已补齐证据，见 real-model-acceptance.md。环境验证不能以代码自审代替。

当前没有已知未解决的重要代码偏差。OpenSpec 8.1 与归档等待 Linux race CI 结果；Docker 可选路径继续明确未验证。
