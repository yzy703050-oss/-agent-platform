> 本文件记录已归档的基础平台 MVP 基线；当前 Go 差旅重构的实现与验收见 [差旅验收](travel-refactor-verification.md)。历史 CI 不代表后续提交。

# 验证记录

## 当前状态（2026-10-23）

MVP 代码、本机交付、真实模型验收与 Linux race CI 已通过。用户选择单代理实施与作者整体自审，授权公开仓库推送。OpenSpec 主规格已同步，变更归档于 changes/archive/2026-10-23-build-agent-platform-mvp。保留 feat/agent-platform-mvp 分支，无 PR 或合并。

## 最终本机自动化证据

首轮执行：pwsh -NoProfile -File .\scripts\verify.ps1；模型验收后的最终回归执行同一脚本加 -SkipNpmInstall，复用此前锁文件安装，84/13 项测试、vet 和构建再次通过（15:34）。日志位于本地 .cache/reports/，不提交日志与凭据。

| 检查 | 实际结果 |
| --- | --- |
| Go 全套测试，真实 PostgreSQL，REQUIRE_DATABASE_TESTS=1 | 84 PASS，0 FAIL，0 SKIP |
| go vet ./... | 通过 |
| Go 服务构建 | 通过，产物 .cache/bin/server-verified.exe |
| npm ci | 通过，锁文件干净安装，无已报告漏洞 |
| React 组件测试 | 13 项通过 |
| TypeScript 与 Vite 生产构建 | 通过 |
| PowerShell 脚本解析 | 全部通过 |
| Compose 与 CI YAML 语法/格式 | Prettier 解析通过；不代表实际运行 |
| OpenSpec strict validate | 通过；只验证正式文档结构 |

PostgreSQL 是真实本机 17.11 服务，集成测试使用独立测试库和随机 schema，测试后清理各自 schema。正式程序不回退内存数据库。

## Eino 恢复与竞态证据

- 使用固定 Eino v0.9.0-beta.1 的真实 ChatModelAgent + TurnLoop，测试模型可控，不连接外部模型 API。
- 实际保存 PostgreSQL Checkpoint，销毁执行对象，再以新 Model/Agent/Runner 从 opaque bytes 恢复；已完成工具不重复。
- 验收中发现 Stop 非阻塞提交导致安全边界竞态。完整测试曾失败一次；单独 100 轮未复现。检查框架源代码后改为等待 TurnContext.Stopped，随后仍等待退出与持久化确认。
- 修复后原有 6 项恢复测试重复 100 轮通过；最终含 2 项流式安全测试的 8 项恢复测试重复 50 轮通过。
- 流式安全工具边界恢复检查通过；强制截流探针曾观察到已完成工具重放，因此平台拒绝保存这种断点，明确 interrupted。不是靠重跑到绿掩盖问题。
- SSE 在已订阅、尚未首次读库时完成任务的真实 HTTP/数据库竞争测试通过，完成事件只出现一次。

## Linux CI 实际证据

[GitHub Actions run 39921638050](https://github.com/yzy703050-oss/-agent-platform/actions/runs/39921638050)，提交 ce6634294a3e887c818cde8ec54a0ce5366c3cd0。已读取两项 job 的完整日志并核对各步骤结果：

- backend job 120308013774：真实 PostgreSQL 服务下 go test -race ./... -count=1 -timeout 180s 全部包通过，集成测试包 32.472s；go vet 与后端构建通过。
- frontend job 120308013338：npm ci、13 项组件测试、TypeScript 与生产构建通过。
- CI 使用合成数据库配置，不需要或上传本地真实模型 Key。Windows 本机 race 仍无通过结果，不能以 Linux 成功改写本机事实。

## 浏览器实际检查

本机 React 连接真实 Go API 与开发库，使用合成验收账户完成登录、创建通用会话、切换创建差旅会话、从历史返回原 Agent、加载设置表单。未配置模型时明确显示聊天不可用；差旅显示尚未接入，无模拟行程。

真实模型已验证回答、工具展示、切换继续执行、pagehide 自动暂停、重开不自动执行、点击继续以 attempt=2 完成，以及暂停后新问题替代。真实关闭短任务可能在租约到期前自然完成，长流停止也实测出现 interrupted；没有把这些结果算作安全断点恢复成功。过程、数据库只读证据和截图见 [真实模型验收](real-model-acceptance.md)。

组件测试覆盖手动继续、暂停后新问替代、历史保持、心跳独立于会话、上次会话重开仅展示、旧 attempt 增量拒绝、切换账号清空旧工作区与设置草稿。

## 整体审查与规格核对

- 作者自审及修复见 [代码审查](code-review.md)。用户选择单代理，未派独立评审。
- 全部 36 个 Scenario 的代码/测试/人工证据映射见 [规格核对](spec-verification.md)。
- 当前 CLI 没有 verify 子命令，项目没有额外 verify 工作流；使用人工逐项核对，未声称运行 OpenSpec verify。
- OpenSpec tasks 与详细实施计划已逐项核对代码和测试。archive 实际同步 5 份主规格、22 个 Requirement、36 个 Scenario；归档后将任务表中“归档操作自身”及其父任务两项标为完成。

## 尚未取得的证据

| 检查 | 当前状态 |
| --- | --- |
| Windows race | 工具链失败；沙箱内 GCC DLL 重定位错误，沙箱外测试程序退出 0xc0000139，无通过证据 |
| Docker Compose/镜像 | 配置已提供，本机无 Docker，未实际构建或运行 |

model-smoke.ps1 及 -ExerciseRecovery 已实际通过，脱敏报告保存在 .cache/reports/model-smoke.json。最初长回答恢复探针失败，修正验收触发点为短工具步骤的安全边界后通过，产品代码未改。密钥只在本地 .env 配置，修改后重启 API，不发送到聊天中。

## 环境

Go 1.27.2、Node 24.20.0、PostgreSQL 17.11、Windows。Go module 最低要求 1.26.0。Sonic 在当前 Go 版本提示超出优化支持范围并回退标准 JSON；常规测试通过，不据此声称已验证优化性能。缓存和报告限定在项目 .cache/；真实配置均被 Git 忽略。
