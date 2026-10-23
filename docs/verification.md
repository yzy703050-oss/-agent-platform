# 验证记录

## 当前状态（2026-10-23）

MVP 代码与本机交付已实现，7/8 个阶段通过；最终真实模型验收未完成。用户选择单代理实施与作者整体自审。OpenSpec 8.1 保持未勾选，未归档、未合并、未推送。

## 最终本机自动化证据

执行：pwsh -NoProfile -File .\scripts\verify.ps1。日志位于本地 .cache/reports/，不提交日志与凭据。

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

## 浏览器实际检查

本机 React 连接真实 Go API 与开发库，使用合成验收账户完成登录、创建通用会话、切换创建差旅会话、从历史返回原 Agent、加载设置表单。未配置模型时明确显示聊天不可用；差旅显示尚未接入，无模拟行程。

组件测试覆盖手动继续、暂停后新问替代、历史保持、心跳独立于会话、上次会话重开仅展示、旧 attempt 增量拒绝、切换账号清空旧工作区与设置草稿。真实模型相关完整浏览器流程仍待执行。

## 整体审查与规格核对

- 作者自审及修复见 [代码审查](code-review.md)。用户选择单代理，未派独立评审。
- 全部 36 个 Scenario 的代码/测试/人工证据映射见 [规格核对](spec-verification.md)。
- 当前 CLI 没有 verify 子命令，项目没有额外 verify 工作流；使用人工逐项核对，未声称运行 OpenSpec verify。
- OpenSpec tasks 与详细实施计划已记录实际完成部分与未完成验收，不仅凭勾选框认定交付。

## 尚未取得的证据

| 检查 | 当前状态 |
| --- | --- |
| 真实模型聊天与只读工具 | 未执行；.env 中三个 LLM 配置项未填写 |
| 真实模型暂停/恢复与完整浏览器任务操作 | 未执行，等待配置 |
| Windows race | 工具链失败；沙箱内 GCC DLL 重定位错误，沙箱外测试程序退出 0xc0000139，无通过证据 |
| Linux race CI | 工作流已提供，未推送或执行，无结果 |
| Docker Compose/镜像 | 配置已提供，本机无 Docker，未实际构建或运行 |

model-smoke.ps1 在缺 LLM_BASE_URL 时按预期拒绝执行；这只证明配置检查，不是模型验收通过。密钥只在本地 .env 配置，修改后重启 API，不发送到聊天中。

## 环境

Go 1.27.2、Node 24.20.0、PostgreSQL 17.11、Windows。Go module 最低要求 1.26.0。Sonic 在当前 Go 版本提示超出优化支持范围并回退标准 JSON；常规测试通过，不据此声称已验证优化性能。缓存和报告限定在项目 .cache/；真实配置均被 Git 忽略。
